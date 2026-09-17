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
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
	"github.com/antoniocali/polaris-k8s/internal/polaris"
)

// PolarisFinalizer is the finalizer all CRDs add before they touch the
// Polaris server, so deletion blocks until the operator has removed the
// remote resource.
const PolarisFinalizer = "polaris.k8s.calific.io/finalizer"

// Condition types used across the API. Kept here so that the test suite and
// every reconciler agree on string values.
const (
	ConditionReady              = "Ready"
	ConditionSynced             = "Synced"
	ConditionAuthValid          = "AuthValid"
	ConditionCredentialsWritten = "CredentialsWritten"
)

// Common Reason values surfaced in status.conditions.
const (
	ReasonReconciling      = "Reconciling"
	ReasonReady            = "Ready"
	ReasonRefError         = "ReferenceError"
	ReasonAuthError        = "AuthenticationError"
	ReasonPolarisError     = "PolarisError"
	ReasonInvalidSpec      = "InvalidSpec"
	ReasonDeleting         = "Deleting"
	ReasonSyncFailed       = "SyncFailed"
	ReasonSynced           = "Synced"
	ReasonCredentialsValid = "CredentialsValid"
	// ReasonDependencyNotReady marks a resource that is waiting for a parent
	// it references to become Ready (e.g. a catalog-role applied before its
	// catalog exists). Not an error — a normal, self-clearing wait state.
	ReasonDependencyNotReady = "DependencyNotReady"
)

// dependencyRequeueAfter is the short, fixed delay used when a resource is
// waiting for a parent to become Ready. Fixed (not exponential) so cold-start
// convergence is quick, and the wait is not logged as a reconcile error.
const dependencyRequeueAfter = 5 * time.Second

// parentReady reports whether a referenced parent's Ready condition is True.
// Dependent reconcilers gate on this so a child applied before its parent
// (e.g. a whole ArgoCD app synced at once) waits quietly instead of hammering
// Polaris with calls that 404 until the parent's server-side object exists.
func parentReady(conds []metav1.Condition) bool {
	return meta.IsStatusConditionTrue(conds, ConditionReady)
}

// waitForDependency records that obj is waiting on a not-yet-Ready parent and
// requeues on a short fixed delay WITHOUT returning an error — so the reconcile
// log stays calm and the object surfaces a clear DependencyNotReady status
// instead of a transient PolarisError.
//
// It deliberately leaves status.observedGeneration untouched: the object hasn't
// converged to its spec yet, so tooling that reads observedGeneration==generation
// as "done" should not see a match while we wait. The status write is also
// skipped when the wait condition is already in place — these reconciles fire
// every dependencyRequeueAfter for each waiting child, and an unconditional
// Status().Update would be one API write per child per tick.
func waitForDependency(
	ctx context.Context,
	sw client.StatusClient,
	obj client.Object,
	conds *[]metav1.Condition,
	gen int64,
	dep string,
) (ctrl.Result, error) {
	msg := fmt.Sprintf("waiting for %s to be ready", dep)
	if cur := meta.FindStatusCondition(*conds, ConditionReady); cur == nil ||
		cur.Status != metav1.ConditionFalse ||
		cur.Reason != ReasonDependencyNotReady ||
		cur.Message != msg ||
		cur.ObservedGeneration != gen {
		setNotReady(conds, gen, ReasonDependencyNotReady, msg)
		if err := sw.Status().Update(ctx, obj); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: dependencyRequeueAfter}, nil
}

// resolveNamespace returns the effective namespace for a ref — the explicit
// value if set, otherwise the referrer's own namespace.
func resolveNamespace(refNamespace, ownerNamespace string) string {
	if refNamespace != "" {
		return refNamespace
	}
	return ownerNamespace
}

// polarisName returns the Polaris-side name for an object: spec.Name when
// set, otherwise metadata.Name. This is the documented defaulting rule the
// CRD schema can't express via markers.
func polarisName(specName, metaName string) string {
	if specName != "" {
		return specName
	}
	return metaName
}

// equalProperties is a strict equality check between two properties maps
// used for drift detection. Authoritative drift policy: any difference
// (added, removed, or changed key) means we need to push spec to Polaris.
//
// A nil map and an empty map are treated as equal — Polaris commonly returns
// a non-nil but empty `{}` properties object while an unset CRD map is nil,
// and reflect.DeepEqual would flag that as perpetual drift, causing a
// reconcile hot-loop that hammers the Polaris API.
func equalProperties(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// --- Reference resolution ---

func getConnection(ctx context.Context, c client.Client, ref polarisv1alpha1.ConnectionRef, defaultNs string) (*polarisv1alpha1.PolarisConnection, error) {
	obj := &polarisv1alpha1.PolarisConnection{}
	key := types.NamespacedName{Name: ref.Name, Namespace: resolveNamespace(ref.Namespace, defaultNs)}
	if err := c.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("resolve PolarisConnection %s: %w", key, err)
	}
	return obj, nil
}

func getCatalog(ctx context.Context, c client.Client, ref polarisv1alpha1.CatalogRef, defaultNs string) (*polarisv1alpha1.PolarisCatalog, error) {
	obj := &polarisv1alpha1.PolarisCatalog{}
	key := types.NamespacedName{Name: ref.Name, Namespace: resolveNamespace(ref.Namespace, defaultNs)}
	if err := c.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("resolve PolarisCatalog %s: %w", key, err)
	}
	return obj, nil
}

func getNamespace(ctx context.Context, c client.Client, ref polarisv1alpha1.NamespaceRef, defaultNs string) (*polarisv1alpha1.PolarisNamespace, error) {
	obj := &polarisv1alpha1.PolarisNamespace{}
	key := types.NamespacedName{Name: ref.Name, Namespace: resolveNamespace(ref.Namespace, defaultNs)}
	if err := c.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("resolve PolarisNamespace %s: %w", key, err)
	}
	return obj, nil
}

func getPrincipal(ctx context.Context, c client.Client, ref polarisv1alpha1.PrincipalRef, defaultNs string) (*polarisv1alpha1.PolarisPrincipal, error) {
	obj := &polarisv1alpha1.PolarisPrincipal{}
	key := types.NamespacedName{Name: ref.Name, Namespace: resolveNamespace(ref.Namespace, defaultNs)}
	if err := c.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("resolve PolarisPrincipal %s: %w", key, err)
	}
	return obj, nil
}

func getPrincipalRole(ctx context.Context, c client.Client, ref polarisv1alpha1.PrincipalRoleRef, defaultNs string) (*polarisv1alpha1.PolarisPrincipalRole, error) {
	obj := &polarisv1alpha1.PolarisPrincipalRole{}
	key := types.NamespacedName{Name: ref.Name, Namespace: resolveNamespace(ref.Namespace, defaultNs)}
	if err := c.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("resolve PolarisPrincipalRole %s: %w", key, err)
	}
	return obj, nil
}

func getCatalogRole(ctx context.Context, c client.Client, ref polarisv1alpha1.CatalogRoleRef, defaultNs string) (*polarisv1alpha1.PolarisCatalogRole, error) {
	obj := &polarisv1alpha1.PolarisCatalogRole{}
	key := types.NamespacedName{Name: ref.Name, Namespace: resolveNamespace(ref.Namespace, defaultNs)}
	if err := c.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("resolve PolarisCatalogRole %s: %w", key, err)
	}
	return obj, nil
}

// --- Polaris client construction ---

// clientBuilderFunc is the function signature each reconciler uses to build
// a *polaris.Client. Production code wires this to buildPolarisClient;
// tests swap it for a builder that returns a *polaris.Client pointing at a
// httptest.Server.
type clientBuilderFunc func(ctx context.Context, c client.Client, conn *polarisv1alpha1.PolarisConnection) (*polaris.Client, error)

// buildPolarisClient resolves the credentials Secret referenced by a
// PolarisConnection and assembles a polaris.Client. Returns a wrapped error
// suitable for status reporting.
func buildPolarisClient(ctx context.Context, c client.Client, conn *polarisv1alpha1.PolarisConnection) (*polaris.Client, error) {
	if conn == nil {
		return nil, errors.New("nil PolarisConnection")
	}

	creds := &corev1.Secret{}
	credsKey := types.NamespacedName{
		Name:      conn.Spec.CredentialsSecretRef.Name,
		Namespace: conn.Namespace,
	}
	if err := c.Get(ctx, credsKey, creds); err != nil {
		return nil, fmt.Errorf("read credentials Secret %s: %w", credsKey, err)
	}

	clientIDKey := conn.Spec.CredentialsSecretRef.ClientIdKey
	if clientIDKey == "" {
		clientIDKey = "clientId"
	}
	clientSecretKey := conn.Spec.CredentialsSecretRef.ClientSecretKey
	if clientSecretKey == "" {
		clientSecretKey = "clientSecret"
	}

	clientID := string(creds.Data[clientIDKey])
	clientSecret := string(creds.Data[clientSecretKey])
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("credentials Secret %s missing keys %q and/or %q", credsKey, clientIDKey, clientSecretKey)
	}

	cfg := polaris.Config{
		ServerURL:          conn.Spec.ServerURL,
		TokenPath:          conn.Spec.TokenPath,
		Scope:              conn.Spec.Scope,
		ClientID:           clientID,
		ClientSecret:       clientSecret,
		InsecureSkipVerify: conn.Spec.InsecureSkipVerify,
	}

	if conn.Spec.CABundleSecretRef != nil {
		caSecret := &corev1.Secret{}
		caKey := types.NamespacedName{
			Name:      conn.Spec.CABundleSecretRef.Name,
			Namespace: conn.Namespace,
		}
		if err := c.Get(ctx, caKey, caSecret); err != nil {
			return nil, fmt.Errorf("read CA bundle Secret %s: %w", caKey, err)
		}
		bundleKey := conn.Spec.CABundleSecretRef.Key
		if bundleKey == "" {
			bundleKey = "ca.crt"
		}
		cfg.CABundle = caSecret.Data[bundleKey]
		if len(cfg.CABundle) == 0 {
			return nil, fmt.Errorf("CA bundle Secret %s missing key %q", caKey, bundleKey)
		}
	}

	return polaris.NewClient(cfg)
}

// --- Namespace path walking ---

// resolveNamespacePath walks the parentRef chain of a PolarisNamespace and
// returns:
//   - the full Polaris-side path (e.g. ["analytics", "sales"]) with the
//     given namespace as the last segment,
//   - the root PolarisCatalog the namespace ultimately belongs to.
//
// It enforces the cross-CRD invariant the schema can't express: every
// ancestor must reference the same catalog as the leaf namespace.
func resolveNamespacePath(ctx context.Context, c client.Client, ns *polarisv1alpha1.PolarisNamespace) ([]string, *polarisv1alpha1.PolarisCatalog, error) {
	if ns == nil {
		return nil, nil, errors.New("nil PolarisNamespace")
	}

	path := []string{polarisName(ns.Spec.Name, ns.Name)}
	leafCatalogName := ns.Spec.CatalogRef.Name
	leafCatalogNS := resolveNamespace(ns.Spec.CatalogRef.Namespace, ns.Namespace)

	cur := ns
	// Prevent infinite loops if someone creates a cyclic parentRef.
	const maxDepth = 32
	for i := 0; cur.Spec.ParentRef != nil; i++ {
		if i >= maxDepth {
			return nil, nil, fmt.Errorf("namespace parentRef chain exceeds %d levels (cycle?)", maxDepth)
		}

		parent := &polarisv1alpha1.PolarisNamespace{}
		parentKey := types.NamespacedName{
			Name:      cur.Spec.ParentRef.Name,
			Namespace: resolveNamespace(cur.Spec.ParentRef.Namespace, cur.Namespace),
		}
		if err := c.Get(ctx, parentKey, parent); err != nil {
			return nil, nil, fmt.Errorf("walk parentRef %s: %w", parentKey, err)
		}

		parentCatNS := resolveNamespace(parent.Spec.CatalogRef.Namespace, parent.Namespace)
		if parent.Spec.CatalogRef.Name != leafCatalogName || parentCatNS != leafCatalogNS {
			return nil, nil, fmt.Errorf(
				"namespace %s/%s references catalog %s/%s but parent %s/%s references catalog %s/%s — chain crosses catalogs",
				ns.Namespace, ns.Name, leafCatalogNS, leafCatalogName,
				parent.Namespace, parent.Name, parentCatNS, parent.Spec.CatalogRef.Name,
			)
		}

		path = append([]string{polarisName(parent.Spec.Name, parent.Name)}, path...)
		cur = parent
	}

	catalog := &polarisv1alpha1.PolarisCatalog{}
	if err := c.Get(ctx, types.NamespacedName{Name: leafCatalogName, Namespace: leafCatalogNS}, catalog); err != nil {
		return nil, nil, fmt.Errorf("resolve root catalog %s/%s: %w", leafCatalogNS, leafCatalogName, err)
	}
	return path, catalog, nil
}

// --- Status condition helpers ---

func setReady(conds *[]metav1.Condition, gen int64, msg string) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: gen,
		Reason:             ReasonReady,
		Message:            msg,
	})
}

func setNotReady(conds *[]metav1.Condition, gen int64, reason, msg string) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: gen,
		Reason:             reason,
		Message:            msg,
	})
}

func setSynced(conds *[]metav1.Condition, gen int64, msg string) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               ConditionSynced,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: gen,
		Reason:             ReasonSynced,
		Message:            msg,
	})
}

func setCondition(conds *[]metav1.Condition, typ string, status metav1.ConditionStatus, gen int64, reason, msg string) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               typ,
		Status:             status,
		ObservedGeneration: gen,
		Reason:             reason,
		Message:            msg,
	})
}

// --- Finalizer helpers ---

// ensureFinalizer adds PolarisFinalizer to obj's metadata if not already
// present, and persists the change. Returns true if the object was patched.
func ensureFinalizer(ctx context.Context, c client.Client, obj client.Object) (bool, error) {
	if controllerutil.ContainsFinalizer(obj, PolarisFinalizer) {
		return false, nil
	}
	controllerutil.AddFinalizer(obj, PolarisFinalizer)
	if err := c.Update(ctx, obj); err != nil {
		return false, fmt.Errorf("add finalizer: %w", err)
	}
	return true, nil
}

// removeFinalizer strips the operator's finalizer and persists. Called from
// the deletion branch after the Polaris-side resource has been removed.
func removeFinalizer(ctx context.Context, c client.Client, obj client.Object) error {
	if !controllerutil.ContainsFinalizer(obj, PolarisFinalizer) {
		return nil
	}
	controllerutil.RemoveFinalizer(obj, PolarisFinalizer)
	if err := c.Update(ctx, obj); err != nil {
		return fmt.Errorf("remove finalizer: %w", err)
	}
	return nil
}
