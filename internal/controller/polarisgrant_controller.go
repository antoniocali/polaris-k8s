/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
	"github.com/antoniocali/polaris-k8s/internal/polaris"
	"github.com/antoniocali/polaris-k8s/internal/polaris/management"
)

// PolarisGrantReconciler reconciles a PolarisGrant (a single privilege on a
// single target attached to a CatalogRole). Idempotent PUT-and-forget.
type PolarisGrantReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisgrants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisgrants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisgrants/finalizers,verbs=update
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogroles;polarisnamespaces;polaristables;polarisviews,verbs=get;list;watch

func (r *PolarisGrantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	g := &polarisv1alpha1.PolarisGrant{}
	if err := r.Get(ctx, req.NamespacedName, g); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// On the delete path, a failure to resolve the connection chain or build
	// the client means the remote is unreachable (the PolarisConnection or its
	// credentials Secret was deleted first — common during namespace teardown).
	// Treat that as "remote already gone" and drop the finalizer rather than
	// wedging the object in Terminating forever.
	deleting := !g.DeletionTimestamp.IsZero()

	cr, err := getCatalogRole(ctx, r.Client, g.Spec.CatalogRoleRef, g.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, g)
		}
		setNotReady(&g.Status.Conditions, g.Generation, ReasonRefError, err.Error())
		g.Status.ObservedGeneration = g.Generation
		if updErr := r.Status().Update(ctx, g); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	// Wait quietly until the parent catalog role is reconciled.
	if !deleting && !parentReady(cr.Status.Conditions) {
		return waitForDependency(ctx, r.Client, g, &g.Status.Conditions, g.Generation, "PolarisCatalogRole "+cr.Name)
	}
	cat, err := getCatalog(ctx, r.Client, cr.Spec.CatalogRef, cr.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, g)
		}
		setNotReady(&g.Status.Conditions, g.Generation, ReasonRefError, err.Error())
		g.Status.ObservedGeneration = g.Generation
		if updErr := r.Status().Update(ctx, g); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}
	conn, err := getConnection(ctx, r.Client, cat.Spec.ConnectionRef, cat.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, g)
		}
		setNotReady(&g.Status.Conditions, g.Generation, ReasonRefError, err.Error())
		g.Status.ObservedGeneration = g.Generation
		if updErr := r.Status().Update(ctx, g); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, g)
		}
		setNotReady(&g.Status.Conditions, g.Generation, ReasonAuthError, err.Error())
		g.Status.ObservedGeneration = g.Generation
		if updErr := r.Status().Update(ctx, g); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	catalogName := polarisName(cat.Spec.Name, cat.Name)
	roleName := polarisName(cr.Spec.Name, cr.Name)

	grantBody, err := r.buildGrantBody(ctx, g)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, g)
		}
		setNotReady(&g.Status.Conditions, g.Generation, ReasonInvalidSpec, err.Error())
		g.Status.ObservedGeneration = g.Generation
		if updErr := r.Status().Update(ctx, g); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, g, pc, catalogName, roleName, grantBody)
	}

	if added, err := ensureFinalizer(ctx, r.Client, g); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	body, err := json.Marshal(map[string]any{"grant": grantBody})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("marshal grant body: %w", err)
	}
	resp, err := pc.Management.AddGrantToCatalogRoleWithBodyWithResponse(ctx, catalogName, roleName, "application/json", bytes.NewReader(body))
	if err != nil {
		setNotReady(&g.Status.Conditions, g.Generation, ReasonPolarisError, err.Error())
		g.Status.ObservedGeneration = g.Generation
		if updErr := r.Status().Update(ctx, g); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, fmt.Errorf("add grant: %w", err)
	}
	// A 404 here means the catalog role or grant target isn't on the server yet
	// — the generated client reports it via StatusCode, not err. Wait quietly.
	if resp.StatusCode() == http.StatusNotFound {
		return waitForDependency(ctx, r.Client, g, &g.Status.Conditions, g.Generation, "grant target (Polaris-side)")
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
		err := &polaris.APIError{StatusCode: resp.StatusCode(), Op: "grants/add", Body: string(resp.Body)}
		setNotReady(&g.Status.Conditions, g.Generation, ReasonPolarisError, err.Error())
		g.Status.ObservedGeneration = g.Generation
		if updErr := r.Status().Update(ctx, g); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	g.Status.ObservedGeneration = g.Generation
	setSynced(&g.Status.Conditions, g.Generation, "grant active")
	setReady(&g.Status.Conditions, g.Generation, "grant is reconciled")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, g)
}

func (r *PolarisGrantReconciler) finalize(ctx context.Context, g *polarisv1alpha1.PolarisGrant, pc *polaris.Client, catalogName, roleName string, grantBody map[string]any) error {
	body, err := json.Marshal(map[string]any{"grant": grantBody})
	if err != nil {
		return fmt.Errorf("marshal revoke body: %w", err)
	}
	resp, err := pc.Management.RevokeGrantFromCatalogRoleWithBodyWithResponse(ctx, catalogName, roleName, &management.RevokeGrantFromCatalogRoleParams{}, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("revoke grant: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "grants/revoke", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, g)
}

// buildGrantBody assembles the Polaris-side grant payload from the CR's
// target discriminator. We resolve the namespace ref into its full path
// (so the body matches what Polaris expects for nested namespaces).
func (r *PolarisGrantReconciler) buildGrantBody(ctx context.Context, g *polarisv1alpha1.PolarisGrant) (map[string]any, error) {
	body := map[string]any{
		"type":      string(g.Spec.Target.Type),
		"privilege": string(g.Spec.Privilege),
	}
	if g.Spec.Target.Type == polarisv1alpha1.GrantTargetCatalog {
		return body, nil
	}

	if g.Spec.Target.NamespaceRef == nil {
		return nil, errors.New("target.namespaceRef is required when target.type is namespace, table, or view")
	}
	ns, err := getNamespace(ctx, r.Client, *g.Spec.Target.NamespaceRef, g.Namespace)
	if err != nil {
		return nil, fmt.Errorf("resolve namespace ref: %w", err)
	}
	path, _, err := resolveNamespacePath(ctx, r.Client, ns)
	if err != nil {
		return nil, fmt.Errorf("resolve namespace path: %w", err)
	}
	body["namespace"] = path

	switch g.Spec.Target.Type {
	case polarisv1alpha1.GrantTargetNamespace:
		// nothing more
	case polarisv1alpha1.GrantTargetTable:
		if g.Spec.Target.TableRef == nil {
			return nil, errors.New("target.tableRef is required when target.type is table")
		}
		// Use spec.name override if set on the table; otherwise metadata.name
		// is the implicit Polaris-side identifier.
		tbl := &polarisv1alpha1.PolarisTable{}
		if err := r.Get(ctx, client.ObjectKey{
			Name:      g.Spec.Target.TableRef.Name,
			Namespace: resolveNamespace(g.Spec.Target.TableRef.Namespace, g.Namespace),
		}, tbl); err != nil {
			return nil, fmt.Errorf("resolve table ref: %w", err)
		}
		body["tableName"] = polarisName(tbl.Spec.Name, tbl.Name)
	case polarisv1alpha1.GrantTargetView:
		if g.Spec.Target.ViewRef == nil {
			return nil, errors.New("target.viewRef is required when target.type is view")
		}
		view := &polarisv1alpha1.PolarisView{}
		if err := r.Get(ctx, client.ObjectKey{
			Name:      g.Spec.Target.ViewRef.Name,
			Namespace: resolveNamespace(g.Spec.Target.ViewRef.Namespace, g.Namespace),
		}, view); err != nil {
			return nil, fmt.Errorf("resolve view ref: %w", err)
		}
		body["viewName"] = polarisName(view.Spec.Name, view.Name)
	default:
		return nil, fmt.Errorf("unsupported target.type %q", g.Spec.Target.Type)
	}
	return body, nil
}

func (r *PolarisGrantReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

func (r *PolarisGrantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisGrant{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polarisgrant").
		Complete(r)
}
