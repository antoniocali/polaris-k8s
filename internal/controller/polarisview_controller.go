/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"fmt"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
	"github.com/antoniocali/polaris-k8s/internal/polaris"
	"github.com/antoniocali/polaris-k8s/internal/polaris/catalog"
)

// PolarisViewReconciler reconciles a PolarisView. Initial pass covers
// create + delete; schema/SQL updates require constructing a CommitView
// payload and are deferred to a follow-up.
type PolarisViewReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisviews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisviews/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisviews/finalizers,verbs=update
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisnamespaces;polariscatalogs,verbs=get;list;watch

func (r *PolarisViewReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	view := &polarisv1alpha1.PolarisView{}
	if err := r.Get(ctx, req.NamespacedName, view); err != nil {
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
	deleting := !view.DeletionTimestamp.IsZero()

	ns, err := getNamespace(ctx, r.Client, view.Spec.NamespaceRef, view.Namespace)
	if err != nil {
		return r.resolveFail(ctx, view, deleting, ReasonRefError, err)
	}

	// Wait quietly until the parent namespace is reconciled.
	if !deleting && !parentReady(ns.Status.Conditions) {
		return waitForDependency(ctx, r.Client, view, &view.Status.Conditions, view.Generation, "PolarisNamespace "+ns.Name)
	}

	path, cat, err := resolveNamespacePath(ctx, r.Client, ns)
	if err != nil {
		return r.resolveFail(ctx, view, deleting, ReasonRefError, err)
	}
	conn, err := getConnection(ctx, r.Client, cat.Spec.ConnectionRef, cat.Namespace)
	if err != nil {
		return r.resolveFail(ctx, view, deleting, ReasonRefError, err)
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		return r.resolveFail(ctx, view, deleting, ReasonAuthError, err)
	}

	catalogName := polarisName(cat.Spec.Name, cat.Name)
	viewName := polarisName(view.Spec.Name, view.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, view, pc, catalogName, path, viewName)
	}

	if added, err := ensureFinalizer(ctx, r.Client, view); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	loadResp, err := pc.Catalog.LoadViewWithResponse(ctx, catalogName, encodeNamespacePath(path), viewName)
	if err != nil {
		setNotReady(&view.Status.Conditions, view.Generation, ReasonPolarisError, err.Error())
		view.Status.ObservedGeneration = view.Generation
		if updErr := r.Status().Update(ctx, view); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, fmt.Errorf("load view: %w", err)
	}

	if loadResp.StatusCode() == http.StatusNotFound {
		body, err := r.buildCreateBody(view, viewName)
		if err != nil {
			setNotReady(&view.Status.Conditions, view.Generation, ReasonInvalidSpec, err.Error())
			view.Status.ObservedGeneration = view.Generation
			if updErr := r.Status().Update(ctx, view); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
		resp, err := pc.Catalog.CreateViewWithResponse(ctx, catalogName, encodeNamespacePath(path), *body)
		if err != nil {
			setNotReady(&view.Status.Conditions, view.Generation, ReasonPolarisError, err.Error())
			view.Status.ObservedGeneration = view.Generation
			if updErr := r.Status().Update(ctx, view); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, fmt.Errorf("create view: %w", err)
		}
		// A 404 here means the parent namespace isn't on the server yet — the
		// generated client reports it via StatusCode, not err. Wait quietly.
		if resp.StatusCode() == http.StatusNotFound {
			return waitForDependency(ctx, r.Client, view, &view.Status.Conditions, view.Generation, "namespace (Polaris-side)")
		}
		if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
			err := &polaris.APIError{StatusCode: resp.StatusCode(), Op: "views/create", Body: string(resp.Body)}
			setNotReady(&view.Status.Conditions, view.Generation, ReasonPolarisError, err.Error())
			view.Status.ObservedGeneration = view.Generation
			if updErr := r.Status().Update(ctx, view); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
	} else if loadResp.StatusCode() >= 400 {
		err := &polaris.APIError{StatusCode: loadResp.StatusCode(), Op: "views/load", Body: string(loadResp.Body)}
		setNotReady(&view.Status.Conditions, view.Generation, ReasonPolarisError, err.Error())
		view.Status.ObservedGeneration = view.Generation
		if updErr := r.Status().Update(ctx, view); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	view.Status.ObservedGeneration = view.Generation
	setSynced(&view.Status.Conditions, view.Generation, "view state matches spec")
	setReady(&view.Status.Conditions, view.Generation, "view is reconciled")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, view)
}

func (r *PolarisViewReconciler) finalize(ctx context.Context, view *polarisv1alpha1.PolarisView, pc *polaris.Client, catalogName string, path []string, viewName string) error {
	resp, err := pc.Catalog.DropViewWithResponse(ctx, catalogName, encodeNamespacePath(path), viewName)
	if err != nil {
		return fmt.Errorf("drop view: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "views/drop", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, view)
}

func (r *PolarisViewReconciler) buildCreateBody(view *polarisv1alpha1.PolarisView, name string) (*catalog.CreateViewJSONRequestBody, error) {
	schema, err := buildIcebergSchema(view.Spec.Schema)
	if err != nil {
		return nil, err
	}
	rep := catalog.ViewRepresentation{}
	if err := rep.MergeSQLViewRepresentation(catalog.SQLViewRepresentation{
		Type:    "sql",
		Dialect: view.Spec.Dialect,
		Sql:     view.Spec.SQL,
	}); err != nil {
		return nil, fmt.Errorf("build SQL representation: %w", err)
	}
	body := &catalog.CreateViewJSONRequestBody{
		Name:   name,
		Schema: schema,
		ViewVersion: catalog.ViewVersion{
			VersionId:        1,
			TimestampMs:      metav1.Now().UnixMilli(),
			SchemaId:         -1,
			DefaultNamespace: catalog.Namespace{},
			Representations:  []catalog.ViewRepresentation{rep},
			Summary:          map[string]string{"engine": view.Spec.Dialect},
		},
		Properties: copyStringMap(view.Spec.Properties),
	}
	return body, nil
}

// resolveFail handles a pre-deletion-check resolution error (ref lookup or
// client build). On the delete path the remote is unreachable, so we drop the
// finalizer rather than wedging the object in Terminating; otherwise we record
// the failure in status and return the error for requeue.
func (r *PolarisViewReconciler) resolveFail(ctx context.Context, view *polarisv1alpha1.PolarisView, deleting bool, reason string, err error) (ctrl.Result, error) {
	if deleting {
		return ctrl.Result{}, removeFinalizer(ctx, r.Client, view)
	}
	setNotReady(&view.Status.Conditions, view.Generation, reason, err.Error())
	view.Status.ObservedGeneration = view.Generation
	if updErr := r.Status().Update(ctx, view); updErr != nil {
		logf.FromContext(ctx).Error(updErr, "update status")
	}
	return ctrl.Result{}, err
}

func (r *PolarisViewReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

func (r *PolarisViewReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisView{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polarisview").
		Complete(r)
}
