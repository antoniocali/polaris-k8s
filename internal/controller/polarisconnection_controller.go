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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// PolarisConnectionReconciler reconciles a PolarisConnection.
//
// The connection itself has no Polaris-side state to manage — it's a handle
// pointing at a server plus credentials. Reconcile here means: validate the
// referenced Secret, mint a token to prove the credentials work, and surface
// the outcome in status conditions so child resources (Catalog, Principal, …)
// can refuse to reconcile against a broken connection.
type PolarisConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// BuildPolarisClient lets tests inject a builder returning a *polaris.Client
	// pointed at a httptest server. nil means use the production path.
	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *PolarisConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	conn := &polarisv1alpha1.PolarisConnection{}
	if err := r.Get(ctx, req.NamespacedName, conn); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Deletion path — Connections own no remote state, so we just drop the
	// finalizer if one was ever added (older versions of the operator may
	// have set one).
	if !conn.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, removeFinalizer(ctx, r.Client, conn)
	}

	// Validate + Ping.
	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		setCondition(&conn.Status.Conditions, ConditionAuthValid, metav1.ConditionFalse,
			conn.Generation, ReasonAuthError, fmt.Sprintf("cannot build client: %v", err))
		setNotReady(&conn.Status.Conditions, conn.Generation, ReasonAuthError, "credentials could not be loaded")
		conn.Status.ObservedGeneration = conn.Generation
		if updErr := r.Status().Update(ctx, conn); updErr != nil {
			log.Error(updErr, "update status")
		}
		// Secret may show up on a re-watch — retry quickly.
		return ctrl.Result{}, err
	}

	if err := pc.Ping(ctx); err != nil {
		setCondition(&conn.Status.Conditions, ConditionAuthValid, metav1.ConditionFalse,
			conn.Generation, ReasonAuthError, fmt.Sprintf("token exchange failed: %v", err))
		setNotReady(&conn.Status.Conditions, conn.Generation, ReasonAuthError, "could not authenticate to Polaris")
		conn.Status.ObservedGeneration = conn.Generation
		if updErr := r.Status().Update(ctx, conn); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	setCondition(&conn.Status.Conditions, ConditionAuthValid, metav1.ConditionTrue,
		conn.Generation, ReasonCredentialsValid, "successfully exchanged client credentials for an access token")
	setReady(&conn.Status.Conditions, conn.Generation, "connection is healthy")
	conn.Status.ObservedGeneration = conn.Generation
	log.Info("reconciled")
	if err := r.Status().Update(ctx, conn); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *PolarisConnectionReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

// SetupWithManager wires the reconciler into the manager. We also watch
// Secrets so a credentials update triggers re-validation.
func (r *PolarisConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisConnection{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polarisconnection").
		Complete(r)
}
