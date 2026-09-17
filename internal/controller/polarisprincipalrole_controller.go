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
	"maps"
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

// PolarisPrincipalRoleReconciler reconciles a PolarisPrincipalRole — a
// server-wide role on the management plane.
type PolarisPrincipalRoleReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipalroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipalroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipalroles/finalizers,verbs=update

func (r *PolarisPrincipalRoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	pr := &polarisv1alpha1.PolarisPrincipalRole{}
	if err := r.Get(ctx, req.NamespacedName, pr); err != nil {
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
	deleting := !pr.DeletionTimestamp.IsZero()

	conn, err := getConnection(ctx, r.Client, pr.Spec.ConnectionRef, pr.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, pr)
		}
		setNotReady(&pr.Status.Conditions, pr.Generation, ReasonRefError, err.Error())
		pr.Status.ObservedGeneration = pr.Generation
		if updErr := r.Status().Update(ctx, pr); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, pr)
		}
		setNotReady(&pr.Status.Conditions, pr.Generation, ReasonAuthError, err.Error())
		pr.Status.ObservedGeneration = pr.Generation
		if updErr := r.Status().Update(ctx, pr); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	// Wait quietly until the parent connection is healthy.
	if !deleting && !parentReady(conn.Status.Conditions) {
		return waitForDependency(ctx, r.Client, pr, &pr.Status.Conditions, pr.Generation, "PolarisConnection "+conn.Name)
	}

	name := polarisName(pr.Spec.Name, pr.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, pr, pc, name)
	}

	if added, err := ensureFinalizer(ctx, r.Client, pr); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	current, found, err := r.getRole(ctx, pc, name)
	if err != nil {
		setNotReady(&pr.Status.Conditions, pr.Generation, ReasonPolarisError, err.Error())
		pr.Status.ObservedGeneration = pr.Generation
		if updErr := r.Status().Update(ctx, pr); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	if !found {
		if err := r.createRole(ctx, pc, name, pr.Spec.Properties); err != nil {
			setNotReady(&pr.Status.Conditions, pr.Generation, ReasonPolarisError, err.Error())
			pr.Status.ObservedGeneration = pr.Generation
			if updErr := r.Status().Update(ctx, pr); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
	} else if r.driftsFrom(pr, current) {
		if err := r.updateRole(ctx, pc, name, current, pr.Spec.Properties); err != nil {
			setNotReady(&pr.Status.Conditions, pr.Generation, ReasonSyncFailed, err.Error())
			pr.Status.ObservedGeneration = pr.Generation
			if updErr := r.Status().Update(ctx, pr); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
	}

	pr.Status.ObservedGeneration = pr.Generation
	setSynced(&pr.Status.Conditions, pr.Generation, "principal role state matches spec")
	setReady(&pr.Status.Conditions, pr.Generation, "principal role is reconciled")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, pr)
}

func (r *PolarisPrincipalRoleReconciler) finalize(ctx context.Context, pr *polarisv1alpha1.PolarisPrincipalRole, pc *polaris.Client, name string) error {
	resp, err := pc.Management.DeletePrincipalRoleWithResponse(ctx, name)
	if err != nil {
		return fmt.Errorf("delete principal role: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principal-roles/delete", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, pr)
}

func (r *PolarisPrincipalRoleReconciler) getRole(ctx context.Context, pc *polaris.Client, name string) (*management.PrincipalRole, bool, error) {
	resp, err := pc.Management.GetPrincipalRoleWithResponse(ctx, name)
	if err != nil {
		return nil, false, fmt.Errorf("get principal role: %w", err)
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode() >= 400 {
		return nil, false, &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principal-roles/get", Body: string(resp.Body)}
	}
	return resp.JSON200, true, nil
}

func (r *PolarisPrincipalRoleReconciler) createRole(ctx context.Context, pc *polaris.Client, name string, props map[string]string) error {
	role := management.PrincipalRole{Name: name}
	if len(props) > 0 {
		p := copyStringMap(props)
		role.Properties = &p
	}
	resp, err := pc.Management.CreatePrincipalRoleWithResponse(ctx, management.CreatePrincipalRoleJSONRequestBody{
		PrincipalRole: &role,
	})
	if err != nil {
		return fmt.Errorf("create principal role: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principal-roles/create", Body: string(resp.Body)}
	}
	return nil
}

func (r *PolarisPrincipalRoleReconciler) updateRole(ctx context.Context, pc *polaris.Client, name string, current *management.PrincipalRole, props map[string]string) error {
	body := management.UpdatePrincipalRoleJSONRequestBody{
		Properties: copyStringMap(props),
	}
	if current != nil && current.EntityVersion != nil {
		body.CurrentEntityVersion = *current.EntityVersion
	}
	resp, err := pc.Management.UpdatePrincipalRoleWithResponse(ctx, name, body)
	if err != nil {
		return fmt.Errorf("update principal role: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principal-roles/update", Body: string(resp.Body)}
	}
	return nil
}

func (r *PolarisPrincipalRoleReconciler) driftsFrom(pr *polarisv1alpha1.PolarisPrincipalRole, current *management.PrincipalRole) bool {
	var currentProps map[string]string
	if current.Properties != nil {
		currentProps = *current.Properties
	}
	return !equalProperties(currentProps, pr.Spec.Properties)
}

func (r *PolarisPrincipalRoleReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

// copyStringMap returns a shallow copy of m, or an empty map when m is nil.
// Used so the Polaris-side request body never aliases spec memory.
func copyStringMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}

func (r *PolarisPrincipalRoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisPrincipalRole{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polarisprincipalrole").
		Complete(r)
}
