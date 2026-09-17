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
	"strings"

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

// PolarisNamespaceReconciler reconciles a PolarisNamespace.
type PolarisNamespaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisnamespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisnamespaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisnamespaces/finalizers,verbs=update
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogs,verbs=get;list;watch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisconnections,verbs=get;list;watch

func (r *PolarisNamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	ns := &polarisv1alpha1.PolarisNamespace{}
	if err := r.Get(ctx, req.NamespacedName, ns); err != nil {
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
	deleting := !ns.DeletionTimestamp.IsZero()

	path, cat, err := resolveNamespacePath(ctx, r.Client, ns)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, ns)
		}
		setNotReady(&ns.Status.Conditions, ns.Generation, ReasonRefError, err.Error())
		ns.Status.ObservedGeneration = ns.Generation
		if updErr := r.Status().Update(ctx, ns); updErr != nil {
			log.Error(updErr, "update status after path error")
		}
		return ctrl.Result{}, err
	}

	// Wait quietly until the parent catalog is reconciled.
	if !deleting && !parentReady(cat.Status.Conditions) {
		return waitForDependency(ctx, r.Client, ns, &ns.Status.Conditions, ns.Generation, "PolarisCatalog "+cat.Name)
	}

	conn, err := getConnection(ctx, r.Client, cat.Spec.ConnectionRef, cat.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, ns)
		}
		setNotReady(&ns.Status.Conditions, ns.Generation, ReasonRefError, err.Error())
		ns.Status.ObservedGeneration = ns.Generation
		if updErr := r.Status().Update(ctx, ns); updErr != nil {
			log.Error(updErr, "update status after connection error")
		}
		return ctrl.Result{}, err
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, ns)
		}
		setNotReady(&ns.Status.Conditions, ns.Generation, ReasonAuthError, err.Error())
		ns.Status.ObservedGeneration = ns.Generation
		if updErr := r.Status().Update(ctx, ns); updErr != nil {
			log.Error(updErr, "update status after auth error")
		}
		return ctrl.Result{}, err
	}

	catalogName := polarisName(cat.Spec.Name, cat.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, ns, pc, catalogName, path)
	}

	if added, err := ensureFinalizer(ctx, r.Client, ns); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	current, found, err := r.getNamespace(ctx, pc, catalogName, path)
	if err != nil {
		setNotReady(&ns.Status.Conditions, ns.Generation, ReasonPolarisError, err.Error())
		ns.Status.ObservedGeneration = ns.Generation
		if updErr := r.Status().Update(ctx, ns); updErr != nil {
			log.Error(updErr, "update status after get error")
		}
		return ctrl.Result{}, err
	}

	if !found {
		if err := r.createNamespace(ctx, pc, catalogName, path, ns.Spec.Properties); err != nil {
			if polaris.IsNotFound(err) {
				return waitForDependency(ctx, r.Client, ns, &ns.Status.Conditions, ns.Generation, "parent namespace/catalog (Polaris-side)")
			}
			setNotReady(&ns.Status.Conditions, ns.Generation, ReasonPolarisError, err.Error())
			ns.Status.ObservedGeneration = ns.Generation
			if updErr := r.Status().Update(ctx, ns); updErr != nil {
				log.Error(updErr, "update status after create error")
			}
			return ctrl.Result{}, err
		}
	} else {
		add, remove := diffProperties(current, ns.Spec.Properties)
		if len(add) > 0 || len(remove) > 0 {
			if err := r.updateProperties(ctx, pc, catalogName, path, add, remove); err != nil {
				setNotReady(&ns.Status.Conditions, ns.Generation, ReasonSyncFailed, err.Error())
				ns.Status.ObservedGeneration = ns.Generation
				if updErr := r.Status().Update(ctx, ns); updErr != nil {
					log.Error(updErr, "update status after properties error")
				}
				return ctrl.Result{}, err
			}
		}
	}

	ns.Status.FullPath = append([]string(nil), path...)
	ns.Status.ObservedGeneration = ns.Generation
	setSynced(&ns.Status.Conditions, ns.Generation, "namespace state matches spec")
	setReady(&ns.Status.Conditions, ns.Generation, "namespace is reconciled")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, ns)
}

func (r *PolarisNamespaceReconciler) finalize(ctx context.Context, ns *polarisv1alpha1.PolarisNamespace, pc *polaris.Client, catalogName string, path []string) error {
	resp, err := pc.Catalog.DropNamespaceWithResponse(ctx, catalogName, encodeNamespacePath(path))
	if err != nil {
		return fmt.Errorf("drop namespace: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusNotFound {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "namespaces/drop", Body: string(resp.Body)}
	}
	return removeFinalizer(ctx, r.Client, ns)
}

func (r *PolarisNamespaceReconciler) getNamespace(ctx context.Context, pc *polaris.Client, catalogName string, path []string) (map[string]string, bool, error) {
	resp, err := pc.Catalog.LoadNamespaceMetadataWithResponse(ctx, catalogName, encodeNamespacePath(path))
	if err != nil {
		return nil, false, fmt.Errorf("load namespace: %w", err)
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode() >= 400 {
		return nil, false, &polaris.APIError{StatusCode: resp.StatusCode(), Op: "namespaces/load", Body: string(resp.Body)}
	}
	if resp.JSON200 == nil || resp.JSON200.Properties == nil {
		return map[string]string{}, true, nil
	}
	return *resp.JSON200.Properties, true, nil
}

func (r *PolarisNamespaceReconciler) createNamespace(ctx context.Context, pc *polaris.Client, catalogName string, path []string, props map[string]string) error {
	body := catalog.CreateNamespaceJSONRequestBody{
		Namespace: append([]string(nil), path...),
	}
	if len(props) > 0 {
		p := maps.Clone(props)
		body.Properties = &p
	}
	resp, err := pc.Catalog.CreateNamespaceWithResponse(ctx, catalogName, body)
	if err != nil {
		return fmt.Errorf("create namespace: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "namespaces/create", Body: string(resp.Body)}
	}
	return nil
}

func (r *PolarisNamespaceReconciler) updateProperties(ctx context.Context, pc *polaris.Client, catalogName string, path []string, updates map[string]string, removals []string) error {
	body := catalog.UpdatePropertiesJSONRequestBody{}
	if len(updates) > 0 {
		u := maps.Clone(updates)
		body.Updates = &u
	}
	if len(removals) > 0 {
		rm := append([]string(nil), removals...)
		body.Removals = &rm
	}
	resp, err := pc.Catalog.UpdatePropertiesWithResponse(ctx, catalogName, encodeNamespacePath(path), body)
	if err != nil {
		return fmt.Errorf("update properties: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "namespaces/update-properties", Body: string(resp.Body)}
	}
	return nil
}

func (r *PolarisNamespaceReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

// encodeNamespacePath joins segments with Iceberg REST's unit separator (\x1F).
// The generated client URL-encodes the resulting string into a single path segment.
func encodeNamespacePath(path []string) catalog.NsParam {
	return strings.Join(path, "\x1f")
}

// diffProperties computes property changes to make observed match desired
// under authoritative drift semantics.
func diffProperties(observed, desired map[string]string) (updates map[string]string, removals []string) {
	updates = map[string]string{}
	for k, v := range desired {
		if cur, ok := observed[k]; !ok || cur != v {
			updates[k] = v
		}
	}
	for k := range observed {
		if _, ok := desired[k]; !ok {
			removals = append(removals, k)
		}
	}
	if len(updates) == 0 {
		updates = nil
	}
	return updates, removals
}

func (r *PolarisNamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisNamespace{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polarisnamespace").
		Complete(r)
}
