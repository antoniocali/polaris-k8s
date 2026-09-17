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
	"github.com/antoniocali/polaris-k8s/internal/polaris/catalog"
)

// PolarisPolicyReconciler reconciles a PolarisPolicy.
type PolarisPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarispolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarispolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarispolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisnamespaces;polariscatalogs,verbs=get;list;watch

//nolint:gocyclo // four ref resolutions + finalizer + create/update/delete branches push complexity to ~32; extracting would just spread the same logic across helpers without making it clearer
func (r *PolarisPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	pol := &polarisv1alpha1.PolarisPolicy{}
	if err := r.Get(ctx, req.NamespacedName, pol); err != nil {
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
	deleting := !pol.DeletionTimestamp.IsZero()

	ns, err := getNamespace(ctx, r.Client, pol.Spec.NamespaceRef, pol.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, pol)
		}
		setNotReady(&pol.Status.Conditions, pol.Generation, ReasonRefError, err.Error())
		pol.Status.ObservedGeneration = pol.Generation
		if updErr := r.Status().Update(ctx, pol); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	// Wait quietly until the parent namespace is reconciled.
	if !deleting && !parentReady(ns.Status.Conditions) {
		return waitForDependency(ctx, r.Client, pol, &pol.Status.Conditions, pol.Generation, "PolarisNamespace "+ns.Name)
	}
	path, cat, err := resolveNamespacePath(ctx, r.Client, ns)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, pol)
		}
		setNotReady(&pol.Status.Conditions, pol.Generation, ReasonRefError, err.Error())
		pol.Status.ObservedGeneration = pol.Generation
		if updErr := r.Status().Update(ctx, pol); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}
	conn, err := getConnection(ctx, r.Client, cat.Spec.ConnectionRef, cat.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, pol)
		}
		setNotReady(&pol.Status.Conditions, pol.Generation, ReasonRefError, err.Error())
		pol.Status.ObservedGeneration = pol.Generation
		if updErr := r.Status().Update(ctx, pol); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, pol)
		}
		setNotReady(&pol.Status.Conditions, pol.Generation, ReasonAuthError, err.Error())
		pol.Status.ObservedGeneration = pol.Generation
		if updErr := r.Status().Update(ctx, pol); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	catalogName := polarisName(cat.Spec.Name, cat.Name)
	policyName := polarisName(pol.Spec.Name, pol.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, pol, pc, catalogName, path, policyName)
	}

	if added, err := ensureFinalizer(ctx, r.Client, pol); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	loadResp, err := pc.Catalog.LoadPolicyWithResponse(ctx, catalogName, encodeNamespacePath(path), policyName)
	if err != nil {
		setNotReady(&pol.Status.Conditions, pol.Generation, ReasonPolarisError, err.Error())
		pol.Status.ObservedGeneration = pol.Generation
		if updErr := r.Status().Update(ctx, pol); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, fmt.Errorf("load policy: %w", err)
	}

	content := string(pol.Spec.Content.Raw)

	if loadResp.StatusCode() == http.StatusNotFound {
		body := catalog.CreatePolicyJSONRequestBody{
			Name:    policyName,
			Type:    pol.Spec.Type,
			Content: &content,
		}
		if pol.Spec.Description != "" {
			d := pol.Spec.Description
			body.Description = &d
		}
		resp, err := pc.Catalog.CreatePolicyWithResponse(ctx, catalogName, encodeNamespacePath(path), body)
		if err != nil {
			setNotReady(&pol.Status.Conditions, pol.Generation, ReasonPolarisError, err.Error())
			pol.Status.ObservedGeneration = pol.Generation
			if updErr := r.Status().Update(ctx, pol); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, fmt.Errorf("create policy: %w", err)
		}
		// A 404 here means the parent namespace isn't on the server yet — the
		// generated client reports it via StatusCode, not err. Wait quietly.
		if resp.StatusCode() == http.StatusNotFound {
			return waitForDependency(ctx, r.Client, pol, &pol.Status.Conditions, pol.Generation, "namespace (Polaris-side)")
		}
		if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
			err := &polaris.APIError{StatusCode: resp.StatusCode(), Op: "policies/create", Body: string(resp.Body)}
			setNotReady(&pol.Status.Conditions, pol.Generation, ReasonPolarisError, err.Error())
			pol.Status.ObservedGeneration = pol.Generation
			if updErr := r.Status().Update(ctx, pol); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
	} else if loadResp.StatusCode() >= 400 {
		err := &polaris.APIError{StatusCode: loadResp.StatusCode(), Op: "policies/load", Body: string(loadResp.Body)}
		setNotReady(&pol.Status.Conditions, pol.Generation, ReasonPolarisError, err.Error())
		pol.Status.ObservedGeneration = pol.Generation
		if updErr := r.Status().Update(ctx, pol); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	} else if r.policyDrifts(loadResp, content, pol.Spec.Description) {
		body := catalog.UpdatePolicyJSONRequestBody{
			Content: &content,
		}
		if pol.Spec.Description != "" {
			d := pol.Spec.Description
			body.Description = &d
		}
		// Pass observed version for optimistic concurrency.
		if loadResp.JSON200 != nil {
			v := loadResp.JSON200.Policy.Version
			body.CurrentPolicyVersion = &v
		}
		resp, err := pc.Catalog.UpdatePolicyWithResponse(ctx, catalogName, encodeNamespacePath(path), policyName, body)
		if err != nil {
			setNotReady(&pol.Status.Conditions, pol.Generation, ReasonPolarisError, err.Error())
			pol.Status.ObservedGeneration = pol.Generation
			if updErr := r.Status().Update(ctx, pol); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, fmt.Errorf("update policy: %w", err)
		}
		if resp.StatusCode() >= 400 {
			err := &polaris.APIError{StatusCode: resp.StatusCode(), Op: "policies/update", Body: string(resp.Body)}
			setNotReady(&pol.Status.Conditions, pol.Generation, ReasonSyncFailed, err.Error())
			pol.Status.ObservedGeneration = pol.Generation
			if updErr := r.Status().Update(ctx, pol); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
	}

	pol.Status.ObservedGeneration = pol.Generation
	setSynced(&pol.Status.Conditions, pol.Generation, "policy state matches spec")
	setReady(&pol.Status.Conditions, pol.Generation, "policy is reconciled")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, pol)
}

func (r *PolarisPolicyReconciler) finalize(ctx context.Context, pol *polarisv1alpha1.PolarisPolicy, pc *polaris.Client, catalogName string, path []string, policyName string) error {
	resp, err := pc.Catalog.DropPolicyWithResponse(ctx, catalogName, encodeNamespacePath(path), policyName, &catalog.DropPolicyParams{})
	if err != nil {
		return fmt.Errorf("drop policy: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "policies/drop", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, pol)
}

// policyDrifts compares the observed policy against the desired content
// and description. Properties drift on the policy body itself isn't part
// of the Polaris policy model.
func (r *PolarisPolicyReconciler) policyDrifts(resp *catalog.LoadPolicyWrapper, desiredContent, desiredDescription string) bool {
	if resp == nil || resp.JSON200 == nil {
		return false
	}
	cur := resp.JSON200.Policy
	if cur.Content == nil || *cur.Content != desiredContent {
		return true
	}
	curDesc := ""
	if cur.Description != nil {
		curDesc = *cur.Description
	}
	return curDesc != desiredDescription
}

func (r *PolarisPolicyReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

func (r *PolarisPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisPolicy{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polarispolicy").
		Complete(r)
}
