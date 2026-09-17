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

// PolarisCatalogRoleReconciler reconciles a PolarisCatalogRole — a role
// scoped to a specific Polaris catalog.
type PolarisCatalogRoleReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogroles/finalizers,verbs=update
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogs,verbs=get;list;watch

func (r *PolarisCatalogRoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	cr := &polarisv1alpha1.PolarisCatalogRole{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
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
	deleting := !cr.DeletionTimestamp.IsZero()

	cat, err := getCatalog(ctx, r.Client, cr.Spec.CatalogRef, cr.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, cr)
		}
		setNotReady(&cr.Status.Conditions, cr.Generation, ReasonRefError, err.Error())
		cr.Status.ObservedGeneration = cr.Generation
		if updErr := r.Status().Update(ctx, cr); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	// Wait quietly until the parent catalog is reconciled, rather than calling
	// Polaris and 404ing until it exists.
	if !deleting && !parentReady(cat.Status.Conditions) {
		return waitForDependency(ctx, r.Client, cr, &cr.Status.Conditions, cr.Generation, "PolarisCatalog "+cat.Name)
	}

	conn, err := getConnection(ctx, r.Client, cat.Spec.ConnectionRef, cat.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, cr)
		}
		setNotReady(&cr.Status.Conditions, cr.Generation, ReasonRefError, err.Error())
		cr.Status.ObservedGeneration = cr.Generation
		if updErr := r.Status().Update(ctx, cr); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, cr)
		}
		setNotReady(&cr.Status.Conditions, cr.Generation, ReasonAuthError, err.Error())
		cr.Status.ObservedGeneration = cr.Generation
		if updErr := r.Status().Update(ctx, cr); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	catalogName := polarisName(cat.Spec.Name, cat.Name)
	roleName := polarisName(cr.Spec.Name, cr.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, cr, pc, catalogName, roleName)
	}

	if added, err := ensureFinalizer(ctx, r.Client, cr); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	current, found, err := r.getRole(ctx, pc, catalogName, roleName)
	if err != nil {
		setNotReady(&cr.Status.Conditions, cr.Generation, ReasonPolarisError, err.Error())
		cr.Status.ObservedGeneration = cr.Generation
		if updErr := r.Status().Update(ctx, cr); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	if !found {
		if err := r.createRole(ctx, pc, catalogName, roleName, cr.Spec.Properties); err != nil {
			if polaris.IsNotFound(err) {
				return waitForDependency(ctx, r.Client, cr, &cr.Status.Conditions, cr.Generation, "catalog "+catalogName+" (Polaris-side)")
			}
			setNotReady(&cr.Status.Conditions, cr.Generation, ReasonPolarisError, err.Error())
			cr.Status.ObservedGeneration = cr.Generation
			if updErr := r.Status().Update(ctx, cr); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
	} else if r.driftsFrom(cr, current) {
		if err := r.updateRole(ctx, pc, catalogName, roleName, current, cr.Spec.Properties); err != nil {
			setNotReady(&cr.Status.Conditions, cr.Generation, ReasonSyncFailed, err.Error())
			cr.Status.ObservedGeneration = cr.Generation
			if updErr := r.Status().Update(ctx, cr); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
	}

	cr.Status.ObservedGeneration = cr.Generation
	setSynced(&cr.Status.Conditions, cr.Generation, "catalog role state matches spec")
	setReady(&cr.Status.Conditions, cr.Generation, "catalog role is reconciled")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, cr)
}

func (r *PolarisCatalogRoleReconciler) finalize(ctx context.Context, cr *polarisv1alpha1.PolarisCatalogRole, pc *polaris.Client, catalogName, roleName string) error {
	resp, err := pc.Management.DeleteCatalogRoleWithResponse(ctx, catalogName, roleName)
	if err != nil {
		return fmt.Errorf("delete catalog role: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "catalog-roles/delete", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, cr)
}

func (r *PolarisCatalogRoleReconciler) getRole(ctx context.Context, pc *polaris.Client, catalogName, roleName string) (*management.CatalogRole, bool, error) {
	resp, err := pc.Management.GetCatalogRoleWithResponse(ctx, catalogName, roleName)
	if err != nil {
		return nil, false, fmt.Errorf("get catalog role: %w", err)
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode() >= 400 {
		return nil, false, &polaris.APIError{StatusCode: resp.StatusCode(), Op: "catalog-roles/get", Body: string(resp.Body)}
	}
	return resp.JSON200, true, nil
}

func (r *PolarisCatalogRoleReconciler) createRole(ctx context.Context, pc *polaris.Client, catalogName, roleName string, props map[string]string) error {
	role := management.CatalogRole{Name: roleName}
	if len(props) > 0 {
		p := copyStringMap(props)
		role.Properties = &p
	}
	resp, err := pc.Management.CreateCatalogRoleWithResponse(ctx, catalogName, management.CreateCatalogRoleJSONRequestBody{
		CatalogRole: &role,
	})
	if err != nil {
		return fmt.Errorf("create catalog role: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "catalog-roles/create", Body: string(resp.Body)}
	}
	return nil
}

func (r *PolarisCatalogRoleReconciler) updateRole(ctx context.Context, pc *polaris.Client, catalogName, roleName string, current *management.CatalogRole, props map[string]string) error {
	body := management.UpdateCatalogRoleJSONRequestBody{
		Properties: copyStringMap(props),
	}
	if current != nil && current.EntityVersion != nil {
		body.CurrentEntityVersion = *current.EntityVersion
	}
	resp, err := pc.Management.UpdateCatalogRoleWithResponse(ctx, catalogName, roleName, body)
	if err != nil {
		return fmt.Errorf("update catalog role: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "catalog-roles/update", Body: string(resp.Body)}
	}
	return nil
}

func (r *PolarisCatalogRoleReconciler) driftsFrom(cr *polarisv1alpha1.PolarisCatalogRole, current *management.CatalogRole) bool {
	var currentProps map[string]string
	if current.Properties != nil {
		currentProps = *current.Properties
	}
	return !equalProperties(currentProps, cr.Spec.Properties)
}

func (r *PolarisCatalogRoleReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

func (r *PolarisCatalogRoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisCatalogRole{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polariscatalogrole").
		Complete(r)
}
