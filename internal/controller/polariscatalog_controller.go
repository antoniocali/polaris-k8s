/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// PolarisCatalogReconciler reconciles a PolarisCatalog.
//
// The reconcile loop:
//   - resolve the connection ref + load auth;
//   - GET the catalog by name; if absent, POST CreateCatalog;
//   - if present, diff properties + storage config and PATCH if drifted;
//   - on deletion, finalize by DELETE'ing the catalog server-side.
type PolarisCatalogReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	BuildPolarisClient clientBuilderFunc
}

// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polariscatalogs/finalizers,verbs=update
// +kubebuilder:rbac:groups=polaris.k8s.calific.io,resources=polarisconnections,verbs=get;list;watch

func (r *PolarisCatalogReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	cat := &polarisv1alpha1.PolarisCatalog{}
	if err := r.Get(ctx, req.NamespacedName, cat); err != nil {
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
	deleting := !cat.DeletionTimestamp.IsZero()

	conn, err := getConnection(ctx, r.Client, cat.Spec.ConnectionRef, cat.Namespace)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, cat)
		}
		setNotReady(&cat.Status.Conditions, cat.Generation, ReasonRefError, err.Error())
		cat.Status.ObservedGeneration = cat.Generation
		if updErr := r.Status().Update(ctx, cat); updErr != nil {
			log.Error(updErr, "update status after ref error")
		}
		return ctrl.Result{}, err
	}

	// Wait quietly until the parent connection is healthy, rather than
	// building a client and failing.
	if !deleting && !parentReady(conn.Status.Conditions) {
		return waitForDependency(ctx, r.Client, cat, &cat.Status.Conditions, cat.Generation, "PolarisConnection "+conn.Name)
	}

	pc, err := r.builder()(ctx, r.Client, conn)
	if err != nil {
		if deleting {
			return ctrl.Result{}, removeFinalizer(ctx, r.Client, cat)
		}
		setNotReady(&cat.Status.Conditions, cat.Generation, ReasonAuthError, err.Error())
		cat.Status.ObservedGeneration = cat.Generation
		if updErr := r.Status().Update(ctx, cat); updErr != nil {
			log.Error(updErr, "update status after auth error")
		}
		return ctrl.Result{}, err
	}

	polarisCatName := polarisName(cat.Spec.Name, cat.Name)

	if deleting {
		return ctrl.Result{}, r.finalize(ctx, cat, pc, polarisCatName)
	}

	if added, err := ensureFinalizer(ctx, r.Client, cat); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{Requeue: true}, nil
	}

	current, found, err := r.getCatalog(ctx, pc, polarisCatName)
	if err != nil {
		setNotReady(&cat.Status.Conditions, cat.Generation, ReasonPolarisError, err.Error())
		cat.Status.ObservedGeneration = cat.Generation
		if updErr := r.Status().Update(ctx, cat); updErr != nil {
			log.Error(updErr, "update status after get error")
		}
		return ctrl.Result{}, err
	}

	if !found {
		if err := r.createCatalog(ctx, pc, cat, polarisCatName); err != nil {
			setNotReady(&cat.Status.Conditions, cat.Generation, ReasonPolarisError, err.Error())
			cat.Status.ObservedGeneration = cat.Generation
			if updErr := r.Status().Update(ctx, cat); updErr != nil {
				log.Error(updErr, "update status after create error")
			}
			return ctrl.Result{}, err
		}
	} else if r.driftsFrom(cat, current) {
		if err := r.updateCatalog(ctx, pc, cat, current); err != nil {
			setNotReady(&cat.Status.Conditions, cat.Generation, ReasonSyncFailed, err.Error())
			cat.Status.ObservedGeneration = cat.Generation
			if updErr := r.Status().Update(ctx, cat); updErr != nil {
				log.Error(updErr, "update status after update error")
			}
			return ctrl.Result{}, err
		}
	}

	cat.Status.PolarisCatalogID = polarisCatName
	cat.Status.ObservedGeneration = cat.Generation
	setSynced(&cat.Status.Conditions, cat.Generation, "catalog state matches spec")
	setReady(&cat.Status.Conditions, cat.Generation, "catalog is reconciled")
	log.Info("reconciled")
	return ctrl.Result{}, r.Status().Update(ctx, cat)
}

func (r *PolarisCatalogReconciler) finalize(ctx context.Context, cat *polarisv1alpha1.PolarisCatalog, pc *polaris.Client, name string) error {
	resp, err := pc.Management.DeleteCatalog(ctx, name)
	if err != nil {
		return fmt.Errorf("delete catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return &polaris.APIError{StatusCode: resp.StatusCode, Op: "catalogs/delete", Body: string(body)}
	}
	return removeFinalizer(ctx, r.Client, cat)
}

func (r *PolarisCatalogReconciler) getCatalog(ctx context.Context, pc *polaris.Client, name string) (*management.Catalog, bool, error) {
	resp, err := pc.Management.GetCatalogWithResponse(ctx, name)
	if err != nil {
		return nil, false, fmt.Errorf("get catalog: %w", err)
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode() >= 400 {
		return nil, false, &polaris.APIError{StatusCode: resp.StatusCode(), Op: "catalogs/get", Body: string(resp.Body)}
	}
	return resp.JSON200, true, nil
}

func (r *PolarisCatalogReconciler) createCatalog(ctx context.Context, pc *polaris.Client, cat *polarisv1alpha1.PolarisCatalog, name string) error {
	body, err := buildCatalogCreateBody(cat, name)
	if err != nil {
		return fmt.Errorf("build create body: %w", err)
	}
	resp, err := pc.Management.CreateCatalogWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create catalog: %w", err)
	}
	if resp.StatusCode() >= 400 && resp.StatusCode() != http.StatusConflict {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "catalogs/create", Body: string(resp.Body)}
	}
	return nil
}

func (r *PolarisCatalogReconciler) updateCatalog(ctx context.Context, pc *polaris.Client, cat *polarisv1alpha1.PolarisCatalog, current *management.Catalog) error {
	body, err := buildCatalogUpdateBody(cat, current)
	if err != nil {
		return fmt.Errorf("build update body: %w", err)
	}
	resp, err := pc.Management.UpdateCatalogWithBodyWithResponse(ctx, polarisName(cat.Spec.Name, cat.Name), "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("update catalog: %w", err)
	}
	if resp.StatusCode() >= 400 {
		return &polaris.APIError{StatusCode: resp.StatusCode(), Op: "catalogs/update", Body: string(resp.Body)}
	}
	return nil
}

// driftsFrom returns true when the desired spec differs from the observed
// Polaris-side state in any field the operator owns. Authoritative drift
// policy: missing keys on the Polaris side count as drift.
func (r *PolarisCatalogReconciler) driftsFrom(cat *polarisv1alpha1.PolarisCatalog, current *management.Catalog) bool {
	if current.Properties.DefaultBaseLocation != cat.Spec.DefaultBaseLocation {
		return true
	}
	if !equalProperties(current.Properties.AdditionalProperties, cat.Spec.Properties) {
		return true
	}
	if string(current.StorageConfigInfo.StorageType) != string(cat.Spec.StorageConfig.StorageType) {
		return true
	}
	var currentLocs []string
	if current.StorageConfigInfo.AllowedLocations != nil {
		currentLocs = *current.StorageConfigInfo.AllowedLocations
	}
	if !equalStringSet(currentLocs, cat.Spec.StorageConfig.AllowedLocations) {
		return true
	}
	// Cloud-specific fields (roleArn, tenantId, gcsServiceAccount) are not
	// surfaced on the generated Catalog struct (the spec's allOf+discriminator
	// wasn't flattened by oapi-codegen). Drift on those fields is invisible
	// here until we extend the read path.
	return false
}

func (r *PolarisCatalogReconciler) builder() clientBuilderFunc {
	if r.BuildPolarisClient != nil {
		return r.BuildPolarisClient
	}
	return buildPolarisClient
}

// --- Body construction (handles the storage discriminator manually) ---

type catalogCreateBody struct {
	Catalog catalogBody `json:"catalog"`
}

type catalogBody struct {
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	Properties        map[string]string `json:"properties"`
	StorageConfigInfo storageBody       `json:"storageConfigInfo"`
}

// storageBody carries the discriminator alongside cloud-specific fields.
// Fields not relevant to the chosen storageType stay zero-valued and
// `omitempty` keeps them off the wire.
type storageBody struct {
	StorageType      string   `json:"storageType"`
	AllowedLocations []string `json:"allowedLocations,omitempty"`

	// S3
	RoleARN    string `json:"roleArn,omitempty"`
	Region     string `json:"region,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
	UserARN    string `json:"userArn,omitempty"`

	// Azure
	TenantID           string `json:"tenantId,omitempty"`
	MultiTenantAppName string `json:"multiTenantAppName,omitempty"`
	ConsentURL         string `json:"consentUrl,omitempty"`

	// GCS
	GCSServiceAccount string `json:"gcsServiceAccount,omitempty"`
}

func buildCatalogCreateBody(cat *polarisv1alpha1.PolarisCatalog, name string) ([]byte, error) {
	props := map[string]string{"default-base-location": cat.Spec.DefaultBaseLocation}
	for k, v := range cat.Spec.Properties {
		if k == "default-base-location" {
			continue
		}
		props[k] = v
	}
	typ := string(cat.Spec.Type)
	if typ == "" {
		typ = string(polarisv1alpha1.CatalogTypeInternal)
	}
	storage, err := buildStorageBody(cat.Spec.StorageConfig)
	if err != nil {
		return nil, err
	}
	return json.Marshal(catalogCreateBody{
		Catalog: catalogBody{
			Name:              name,
			Type:              typ,
			Properties:        props,
			StorageConfigInfo: storage,
		},
	})
}

type catalogUpdateBody struct {
	CurrentEntityVersion *int              `json:"currentEntityVersion,omitempty"`
	Properties           map[string]string `json:"properties,omitempty"`
	StorageConfigInfo    *storageBody      `json:"storageConfigInfo,omitempty"`
}

func buildCatalogUpdateBody(cat *polarisv1alpha1.PolarisCatalog, current *management.Catalog) ([]byte, error) {
	props := map[string]string{"default-base-location": cat.Spec.DefaultBaseLocation}
	for k, v := range cat.Spec.Properties {
		if k == "default-base-location" {
			continue
		}
		props[k] = v
	}
	storage, err := buildStorageBody(cat.Spec.StorageConfig)
	if err != nil {
		return nil, err
	}
	body := catalogUpdateBody{
		Properties:        props,
		StorageConfigInfo: &storage,
	}
	if current != nil && current.EntityVersion != nil {
		v := *current.EntityVersion
		body.CurrentEntityVersion = &v
	}
	return json.Marshal(body)
}

func buildStorageBody(sc polarisv1alpha1.StorageConfig) (storageBody, error) {
	out := storageBody{
		StorageType:      string(sc.StorageType),
		AllowedLocations: append([]string(nil), sc.AllowedLocations...),
	}
	switch sc.StorageType {
	case polarisv1alpha1.StorageTypeS3:
		if sc.S3 == nil {
			return storageBody{}, errors.New("storageConfig.s3 must be set when storageType=S3")
		}
		out.RoleARN = sc.S3.RoleARN
		out.Region = sc.S3.Region
		out.ExternalID = sc.S3.ExternalID
		out.UserARN = sc.S3.UserARN
	case polarisv1alpha1.StorageTypeAzure:
		if sc.Azure == nil {
			return storageBody{}, errors.New("storageConfig.azure must be set when storageType=AZURE")
		}
		out.TenantID = sc.Azure.TenantID
		out.MultiTenantAppName = sc.Azure.MultiTenantAppName
		out.ConsentURL = sc.Azure.ConsentURL
	case polarisv1alpha1.StorageTypeGCS:
		if sc.GCS == nil {
			return storageBody{}, errors.New("storageConfig.gcs must be set when storageType=GCS")
		}
		out.GCSServiceAccount = sc.GCS.GCSServiceAccount
	case polarisv1alpha1.StorageTypeFile:
		// FILE (local filesystem, testing only) carries no cloud-specific
		// fields — just storageType + allowedLocations, already set above.
	default:
		return storageBody{}, fmt.Errorf("unsupported storageType %q", sc.StorageType)
	}
	return out, nil
}

func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			return false
		}
	}
	return true
}

func (r *PolarisCatalogReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&polarisv1alpha1.PolarisCatalog{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("polariscatalog").
		Complete(r)
}
