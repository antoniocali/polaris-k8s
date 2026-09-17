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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
	"github.com/antoniocali/polaris-k8s/internal/polaris"
	"github.com/antoniocali/polaris-k8s/internal/polaris/management"
)

// PolarisPrincipalReconciler reconciles a PolarisPrincipal.
//
// Lifecycle differences from the role reconcilers:
//   - on create, Polaris returns generated client credentials. We persist
//     them in the user-named Secret (owned by the CR for K8s GC).
//   - rotation is opt-in: spec.credentialRotationRequired=true triggers a
//     RotateCredentials call and a Secret update.
type PolarisPrincipalReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisprincipals/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

func (r *PolarisPrincipalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	pp := &polarisv1alpha1.PolarisPrincipal{}
	if err := r.Get(ctx, req.NamespacedName, pp); err != nil {
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
	deleting := !pp.DeletionTimestamp.IsZero()

	conn, err := getConnection(ctx, r.Client, pp.Spec.ConnectionRef, pp.Namespace)
	if err != nil {
		return r.resolveFail(ctx, pp, deleting, ReasonRefError, err)
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		return r.resolveFail(ctx, pp, deleting, ReasonAuthError, err)
	}

	// Wait quietly until the parent connection is healthy.
	if !deleting && !parentReady(conn.Status.Conditions) {
		return waitForDependency(ctx, r.Client, pp, &pp.Status.Conditions, pp.Generation, "PolarisConnection "+conn.Name)
	}

	name := polarisName(pp.Spec.Name, pp.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, pp, pc, name)
	}

	if added, err := ensureFinalizer(ctx, r.Client, pp); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	current, found, err := r.getPrincipal(ctx, pc, name)
	if err != nil {
		setNotReady(&pp.Status.Conditions, pp.Generation, ReasonPolarisError, err.Error())
		pp.Status.ObservedGeneration = pp.Generation
		if updErr := r.Status().Update(ctx, pp); updErr != nil {
			log.Error(updErr, "update status")
		}
		return ctrl.Result{}, err
	}

	justCreated := false
	if !found {
		justCreated = true
		creds, err := r.createPrincipal(ctx, pc, pp, name)
		if err != nil {
			setNotReady(&pp.Status.Conditions, pp.Generation, ReasonPolarisError, err.Error())
			pp.Status.ObservedGeneration = pp.Generation
			if updErr := r.Status().Update(ctx, pp); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
		if err := r.writeCredentialsSecret(ctx, pp, creds); err != nil {
			setCondition(&pp.Status.Conditions, ConditionCredentialsWritten, metav1.ConditionFalse,
				pp.Generation, ReasonSyncFailed, err.Error())
			setNotReady(&pp.Status.Conditions, pp.Generation, ReasonSyncFailed, err.Error())
			pp.Status.ObservedGeneration = pp.Generation
			if updErr := r.Status().Update(ctx, pp); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
		now := metav1.Now()
		pp.Status.CredentialsLastRotated = &now
		setCondition(&pp.Status.Conditions, ConditionCredentialsWritten, metav1.ConditionTrue,
			pp.Generation, ReasonSynced, "credentials written to Secret")
	} else if r.driftsFrom(pp, current) {
		if err := r.updatePrincipal(ctx, pc, pp, name, current); err != nil {
			setNotReady(&pp.Status.Conditions, pp.Generation, ReasonSyncFailed, err.Error())
			pp.Status.ObservedGeneration = pp.Generation
			if updErr := r.Status().Update(ctx, pp); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
	}

	// Rotation: when the user flips credentialRotationRequired=true, call
	// RotateCredentials and persist the fresh credentials. The CRD field
	// then stays true until the user toggles it off; the reconciler tracks
	// the "we already rotated for this generation" state via observedGeneration.
	//
	// Skip this on the create path: createPrincipal already passes
	// CredentialRotationRequired through to Polaris and the initial response
	// carries the rotated credentials we just wrote. Rotating again here would
	// invalidate that freshly issued credential and write the Secret twice in
	// a single reconcile.
	if !justCreated && pp.Spec.CredentialRotationRequired && pp.Status.ObservedGeneration < pp.Generation {
		newCreds, err := r.rotateCredentials(ctx, pc, name)
		if err != nil {
			setNotReady(&pp.Status.Conditions, pp.Generation, ReasonSyncFailed, err.Error())
			if updErr := r.Status().Update(ctx, pp); updErr != nil {
				log.Error(updErr, "update status")
			}
			return ctrl.Result{}, err
		}
		if newCreds != nil {
			if err := r.writeCredentialsSecret(ctx, pp, newCreds); err != nil {
				setCondition(&pp.Status.Conditions, ConditionCredentialsWritten, metav1.ConditionFalse,
					pp.Generation, ReasonSyncFailed, err.Error())
				if updErr := r.Status().Update(ctx, pp); updErr != nil {
					log.Error(updErr, "update status")
				}
				return ctrl.Result{}, err
			}
			now := metav1.Now()
			pp.Status.CredentialsLastRotated = &now
		}
	}

	// The management Principal struct surfaces the OAuth clientId rather
	// than a distinct numeric id; we use that as the human-meaningful
	// identifier in status.
	if current != nil && current.ClientId != nil {
		pp.Status.PrincipalID = *current.ClientId
	}
	pp.Status.ObservedGeneration = pp.Generation
	setSynced(&pp.Status.Conditions, pp.Generation, "principal state matches spec")
	setReady(&pp.Status.Conditions, pp.Generation, "principal is reconciled")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, pp)
}

// principalCredentials captures the clientId/clientSecret returned by Polaris
// in create or rotate responses. We use a local struct so the reconciler
// doesn't have to thread the generated anonymous struct type around.
type principalCredentials struct {
	ClientID     string
	ClientSecret string
}

func (r *PolarisPrincipalReconciler) finalize(ctx context.Context, pp *polarisv1alpha1.PolarisPrincipal, pc *polaris.Client, name string) error {
	resp, err := pc.Management.DeletePrincipalWithResponse(ctx, name)
	if err != nil {
		return fmt.Errorf("delete principal: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principals/delete", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, pp)
}

func (r *PolarisPrincipalReconciler) getPrincipal(ctx context.Context, pc *polaris.Client, name string) (*management.Principal, bool, error) {
	resp, err := pc.Management.GetPrincipalWithResponse(ctx, name)
	if err != nil {
		return nil, false, fmt.Errorf("get principal: %w", err)
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode() >= 400 {
		return nil, false, &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principals/get", Body: string(resp.Body)}
	}
	return resp.JSON200, true, nil
}

func (r *PolarisPrincipalReconciler) createPrincipal(ctx context.Context, pc *polaris.Client, pp *polarisv1alpha1.PolarisPrincipal, name string) (*principalCredentials, error) {
	principal := management.Principal{Name: name}
	if len(pp.Spec.Properties) > 0 {
		p := copyStringMap(pp.Spec.Properties)
		principal.Properties = &p
	}
	rot := pp.Spec.CredentialRotationRequired
	body := management.CreatePrincipalJSONRequestBody{
		Principal:                  &principal,
		CredentialRotationRequired: &rot,
	}
	resp, err := pc.Management.CreatePrincipalWithResponse(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("create principal: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
		return nil, &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principals/create", Body: string(resp.Body)}
	}
	if resp.JSON201 == nil {
		// 409 (already exists) — no credentials returned. Caller proceeds
		// to the drift-check path; the existing Secret stays as-is.
		return nil, nil
	}
	return creds(resp.JSON201.Credentials.ClientId, resp.JSON201.Credentials.ClientSecret), nil
}

func (r *PolarisPrincipalReconciler) updatePrincipal(ctx context.Context, pc *polaris.Client, pp *polarisv1alpha1.PolarisPrincipal, name string, current *management.Principal) error {
	body := management.UpdatePrincipalJSONRequestBody{
		Properties: copyStringMap(pp.Spec.Properties),
	}
	if current != nil && current.EntityVersion != nil {
		body.CurrentEntityVersion = *current.EntityVersion
	}
	resp, err := pc.Management.UpdatePrincipalWithResponse(ctx, name, body)
	if err != nil {
		return fmt.Errorf("update principal: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principals/update", Body: string(resp.Body)}
	}
	return nil
}

func (r *PolarisPrincipalReconciler) rotateCredentials(ctx context.Context, pc *polaris.Client, name string) (*principalCredentials, error) {
	resp, err := pc.Management.RotateCredentialsWithResponse(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("rotate credentials: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return nil, &polaris.APIError{StatusCode: resp.StatusCode(), Op: "principals/rotate", Body: string(resp.Body)}
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	return creds(resp.JSON200.Credentials.ClientId, resp.JSON200.Credentials.ClientSecret), nil
}

func (r *PolarisPrincipalReconciler) writeCredentialsSecret(ctx context.Context, pp *polarisv1alpha1.PolarisPrincipal, c *principalCredentials) error {
	if c == nil || c.ClientID == "" || c.ClientSecret == "" {
		return fmt.Errorf("polaris did not return clientId/clientSecret")
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pp.Spec.CredentialsSecretRef.Name,
			Namespace: pp.Namespace,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(pp, secret, r.Scheme); err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data["clientId"] = []byte(c.ClientID)
		secret.Data["clientSecret"] = []byte(c.ClientSecret)
		return nil
	})
	if err != nil {
		return fmt.Errorf("write credentials Secret: %w", err)
	}
	logf.FromContext(ctx).Info("credentials Secret reconciled", "op", op, "secret", types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace})
	return nil
}

func (r *PolarisPrincipalReconciler) driftsFrom(pp *polarisv1alpha1.PolarisPrincipal, current *management.Principal) bool {
	var currentProps map[string]string
	if current.Properties != nil {
		currentProps = *current.Properties
	}
	return !equalProperties(currentProps, pp.Spec.Properties)
}

// resolveFail handles a pre-deletion-check resolution error (ref lookup or
// client build). On the delete path the remote is unreachable, so we drop the
// finalizer rather than wedging the object in Terminating; otherwise we record
// the failure in status and return the error for requeue.
func (r *PolarisPrincipalReconciler) resolveFail(ctx context.Context, pp *polarisv1alpha1.PolarisPrincipal, deleting bool, reason string, err error) (ctrl.Result, error) {
	if deleting {
		return ctrl.Result{}, removeFinalizer(ctx, r.Client, pp)
	}
	setNotReady(&pp.Status.Conditions, pp.Generation, reason, err.Error())
	pp.Status.ObservedGeneration = pp.Generation
	if updErr := r.Status().Update(ctx, pp); updErr != nil {
		logf.FromContext(ctx).Error(updErr, "update status")
	}
	return ctrl.Result{}, err
}

func (r *PolarisPrincipalReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

func creds(id, secret *string) *principalCredentials {
	out := &principalCredentials{}
	if id != nil {
		out.ClientID = *id
	}
	if secret != nil {
		out.ClientSecret = *secret
	}
	return out
}

func (r *PolarisPrincipalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisPrincipal{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.Secret{}).
		Named("polarisprincipal").
		Complete(r)
}
