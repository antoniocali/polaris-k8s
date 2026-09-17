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

// PolarisPrincipalRoleBindingReconciler binds a Principal to a PrincipalRole
// via Polaris's idempotent assignment endpoint.
type PolarisPrincipalRoleBindingReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipalrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipalrolebindings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipalrolebindings/finalizers,verbs=update
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipals;polarisprincipalroles,verbs=get;list;watch

func (r *PolarisPrincipalRoleBindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	b := &polarisv1alpha1.PolarisPrincipalRoleBinding{}
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

	principal, err := getPrincipal(ctx, r.Client, b.Spec.PrincipalRef, b.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, b)
		}
		setNotReady(&b.Status.Conditions, b.Generation, ReasonRefError, err.Error())
		b.Status.ObservedGeneration = b.Generation
		if updErr := r.Status().Update(ctx, b); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}
	if !deleting && !parentReady(principal.Status.Conditions) {
		return waitForDependency(ctx, r.Client, b, &b.Status.Conditions, b.Generation, "PolarisPrincipal "+principal.Name)
	}
	role, err := getPrincipalRole(ctx, r.Client, b.Spec.PrincipalRoleRef, b.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, b)
		}
		setNotReady(&b.Status.Conditions, b.Generation, ReasonRefError, err.Error())
		b.Status.ObservedGeneration = b.Generation
		if updErr := r.Status().Update(ctx, b); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}
	if !deleting && !parentReady(role.Status.Conditions) {
		return waitForDependency(ctx, r.Client, b, &b.Status.Conditions, b.Generation, "PolarisPrincipalRole "+role.Name)
	}
	conn, err := getConnection(ctx, r.Client, role.Spec.ConnectionRef, role.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, b)
		}
		setNotReady(&b.Status.Conditions, b.Generation, ReasonRefError, err.Error())
		b.Status.ObservedGeneration = b.Generation
		if updErr := r.Status().Update(ctx, b); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, b)
		}
		setNotReady(&b.Status.Conditions, b.Generation, ReasonAuthError, err.Error())
		b.Status.ObservedGeneration = b.Generation
		if updErr := r.Status().Update(ctx, b); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	principalName := polarisName(principal.Spec.Name, principal.Name)
	roleName := polarisName(role.Spec.Name, role.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, b, pc, principalName, roleName)
	}

	if added, err := ensureFinalizer(ctx, r.Client, b); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	// Idempotent PUT.
	body := management.AssignPrincipalRoleJSONRequestBody{
		PrincipalRole: &management.PrincipalRole{Name: roleName},
	}
	resp, err := pc.Management.AssignPrincipalRoleWithResponse(ctx, principalName, body)
	if err != nil {
		setNotReady(&b.Status.Conditions, b.Generation, ReasonPolarisError, err.Error())
		b.Status.ObservedGeneration = b.Generation
		if updErr := r.Status().Update(ctx, b); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, fmt.Errorf("assign principal role: %w", err)
	}
	// A 404 here means the principal or principal-role isn't on the server yet
	// — the generated client reports it via StatusCode, not err. Wait quietly.
	if resp.StatusCode() == http.StatusNotFound {
		return waitForDependency(ctx, r.Client, b, &b.Status.Conditions, b.Generation, "principal/principal-role (Polaris-side)")
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
		err := &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principal-role-binding/assign", Body: string(resp.Body)}
		setNotReady(&b.Status.Conditions, b.Generation, ReasonPolarisError, err.Error())
		b.Status.ObservedGeneration = b.Generation
		if updErr := r.Status().Update(ctx, b); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	b.Status.ObservedGeneration = b.Generation
	setSynced(&b.Status.Conditions, b.Generation, "principal role binding active")
	setReady(&b.Status.Conditions, b.Generation, "principal is assigned the role")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, b)
}

func (r *PolarisPrincipalRoleBindingReconciler) finalize(ctx context.Context, b *polarisv1alpha1.PolarisPrincipalRoleBinding, pc *polaris.Client, principalName, roleName string) error {
	resp, err := pc.Management.RevokePrincipalRoleWithResponse(ctx, principalName, roleName)
	if err != nil {
		return fmt.Errorf("revoke principal role: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principal-role-binding/revoke", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, b)
}

func (r *PolarisPrincipalRoleBindingReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

func (r *PolarisPrincipalRoleBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisPrincipalRoleBinding{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polarisprincipalrolebinding").
		Complete(r)
}
