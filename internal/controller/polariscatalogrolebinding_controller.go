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

// PolarisCatalogRoleBindingReconciler binds a PrincipalRole to a CatalogRole
// (the bridge that lets a principal-role holder inherit catalog privileges).
type PolarisCatalogRoleBindingReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogrolebindings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogrolebindings/finalizers,verbs=update
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipalroles;polariscatalogroles,verbs=get;list;watch

func (r *PolarisCatalogRoleBindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	b := &polarisv1alpha1.PolarisCatalogRoleBinding{}
	if err := r.Get(ctx, req.NamespacedName, b); err != nil {
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
	deleting := !b.DeletionTimestamp.IsZero()

	pr, err := getPrincipalRole(ctx, r.Client, b.Spec.PrincipalRoleRef, b.Namespace)
	if err != nil {
		return r.resolveFail(ctx, b, deleting, ReasonRefError, err)
	}
	if !deleting && !parentReady(pr.Status.Conditions) {
		return waitForDependency(ctx, r.Client, b, &b.Status.Conditions, b.Generation, "PolarisPrincipalRole "+pr.Name)
	}
	cr, err := getCatalogRole(ctx, r.Client, b.Spec.CatalogRoleRef, b.Namespace)
	if err != nil {
		return r.resolveFail(ctx, b, deleting, ReasonRefError, err)
	}
	if !deleting && !parentReady(cr.Status.Conditions) {
		return waitForDependency(ctx, r.Client, b, &b.Status.Conditions, b.Generation, "PolarisCatalogRole "+cr.Name)
	}
	cat, err := getCatalog(ctx, r.Client, cr.Spec.CatalogRef, cr.Namespace)
	if err != nil {
		return r.resolveFail(ctx, b, deleting, ReasonRefError, err)
	}
	conn, err := getConnection(ctx, r.Client, cat.Spec.ConnectionRef, cat.Namespace)
	if err != nil {
		return r.resolveFail(ctx, b, deleting, ReasonRefError, err)
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		return r.resolveFail(ctx, b, deleting, ReasonAuthError, err)
	}

	principalRoleName := polarisName(pr.Spec.Name, pr.Name)
	catalogRoleName := polarisName(cr.Spec.Name, cr.Name)
	catalogName := polarisName(cat.Spec.Name, cat.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, b, pc, principalRoleName, catalogName, catalogRoleName)
	}

	if added, err := ensureFinalizer(ctx, r.Client, b); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	body := management.AssignCatalogRoleToPrincipalRoleJSONRequestBody{
		CatalogRole: &management.CatalogRole{Name: catalogRoleName},
	}
	resp, err := pc.Management.AssignCatalogRoleToPrincipalRoleWithResponse(ctx, principalRoleName, catalogName, body)
	if err != nil {
		setNotReady(&b.Status.Conditions, b.Generation, ReasonPolarisError, err.Error())
		b.Status.ObservedGeneration = b.Generation
		if updErr := r.Status().Update(ctx, b); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, fmt.Errorf("assign catalog role: %w", err)
	}
	// A 404 here means the principal-role or catalog-role isn't on the server
	// yet — the generated client reports it via StatusCode, not err. Wait quietly.
	if resp.StatusCode() == http.StatusNotFound {
		return waitForDependency(ctx, r.Client, b, &b.Status.Conditions, b.Generation, "principal-role/catalog-role (Polaris-side)")
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
		err := &polaris.APIError{StatusCode: resp.StatusCode(), Op: "catalog-role-binding/assign", Body: string(resp.Body)}
		setNotReady(&b.Status.Conditions, b.Generation, ReasonPolarisError, err.Error())
		b.Status.ObservedGeneration = b.Generation
		if updErr := r.Status().Update(ctx, b); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	b.Status.ObservedGeneration = b.Generation
	setSynced(&b.Status.Conditions, b.Generation, "catalog role binding active")
	setReady(&b.Status.Conditions, b.Generation, "catalog role is attached to principal role")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, b)
}

func (r *PolarisCatalogRoleBindingReconciler) finalize(ctx context.Context, b *polarisv1alpha1.PolarisCatalogRoleBinding, pc *polaris.Client, principalRoleName, catalogName, catalogRoleName string) error {
	resp, err := pc.Management.RevokeCatalogRoleFromPrincipalRoleWithResponse(ctx, principalRoleName, catalogName, catalogRoleName)
	if err != nil {
		return fmt.Errorf("revoke catalog role: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "catalog-role-binding/revoke", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, b)
}

// resolveFail handles a pre-deletion-check resolution error (ref lookup or
// client build). On the delete path the remote is unreachable, so we drop the
// finalizer rather than wedging the object in Terminating; otherwise we record
// the failure in status and return the error for requeue.
func (r *PolarisCatalogRoleBindingReconciler) resolveFail(ctx context.Context, b *polarisv1alpha1.PolarisCatalogRoleBinding, deleting bool, reason string, err error) (ctrl.Result, error) {
	if deleting {
		return ctrl.Result{}, removeFinalizer(ctx, r.Client, b)
	}
	setNotReady(&b.Status.Conditions, b.Generation, reason, err.Error())
	b.Status.ObservedGeneration = b.Generation
	if updErr := r.Status().Update(ctx, b); updErr != nil {
		logf.FromContext(ctx).Error(updErr, "update status")
	}
	return ctrl.Result{}, err
}

func (r *PolarisCatalogRoleBindingReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

func (r *PolarisCatalogRoleBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisCatalogRoleBinding{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polariscatalogrolebinding").
		Complete(r)
}
