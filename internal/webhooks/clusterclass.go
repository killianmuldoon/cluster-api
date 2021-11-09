/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhooks

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/feature"
	"sigs.k8s.io/cluster-api/internal/topology/check"
	"sigs.k8s.io/cluster-api/internal/topology/variables"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func (webhook *ClusterClass) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&clusterv1.ClusterClass{}).
		WithDefaulter(webhook).
		WithValidator(webhook).
		Complete()
}

// +kubebuilder:webhook:verbs=create;update,path=/validate-cluster-x-k8s-io-v1beta1-clusterclass,mutating=false,failurePolicy=fail,matchPolicy=Equivalent,groups=cluster.x-k8s.io,resources=clusterclasses,versions=v1beta1,name=validation.clusterclass.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1
// +kubebuilder:webhook:verbs=create;update,path=/mutate-cluster-x-k8s-io-v1beta1-clusterclass,mutating=true,failurePolicy=fail,matchPolicy=Equivalent,groups=cluster.x-k8s.io,resources=clusterclasses,versions=v1beta1,name=default.clusterclass.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

// ClusterClass implements a validation and defaulting webhook for ClusterClass.
type ClusterClass struct {
	Client client.Reader
}

var _ webhook.CustomDefaulter = &ClusterClass{}
var _ webhook.CustomValidator = &ClusterClass{}

// Default implements defaulting for ClusterClass create and update.
func (webhook *ClusterClass) Default(_ context.Context, obj runtime.Object) error {
	in, ok := obj.(*clusterv1.ClusterClass)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a ClusterClass but got a %T", obj))
	}
	// Default all namespaces in the references to the object namespace.
	defaultNamespace(in.Spec.Infrastructure.Ref, in.Namespace)
	defaultNamespace(in.Spec.ControlPlane.Ref, in.Namespace)

	if in.Spec.ControlPlane.MachineInfrastructure != nil {
		defaultNamespace(in.Spec.ControlPlane.MachineInfrastructure.Ref, in.Namespace)
	}

	for i := range in.Spec.Workers.MachineDeployments {
		defaultNamespace(in.Spec.Workers.MachineDeployments[i].Template.Bootstrap.Ref, in.Namespace)
		defaultNamespace(in.Spec.Workers.MachineDeployments[i].Template.Infrastructure.Ref, in.Namespace)
	}
	return nil
}

func defaultNamespace(ref *corev1.ObjectReference, namespace string) {
	if ref != nil && len(ref.Namespace) == 0 {
		ref.Namespace = namespace
	}
}

// ValidateCreate implements validation for ClusterClass create.
func (webhook *ClusterClass) ValidateCreate(_ context.Context, obj runtime.Object) error {
	in, ok := obj.(*clusterv1.ClusterClass)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a ClusterClass but got a %T", obj))
	}
	return webhook.validate(nil, in)
}

// ValidateUpdate implements validation for ClusterClass update.
func (webhook *ClusterClass) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) error {
	newClusterClass, ok := newObj.(*clusterv1.ClusterClass)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a ClusterClass but got a %T", newObj))
	}
	oldClusterClass, ok := oldObj.(*clusterv1.ClusterClass)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a ClusterClass but got a %T", oldObj))
	}
	return webhook.validate(oldClusterClass, newClusterClass)
}

// ValidateDelete implements validation for ClusterClass delete.
func (webhook *ClusterClass) ValidateDelete(ctx context.Context, obj runtime.Object) error {
	clusterClass, ok := obj.(*clusterv1.ClusterClass)
	if !ok {
		return apierrors.NewBadRequest(fmt.Sprintf("expected a ClusterClass but got a %T", obj))
	}
	clusters, err := webhook.clustersUsingClusterClass(clusterClass)
	if err != nil {
		return err.ToAggregate()
	}
	if len(clusters) != 0 {
		// TODO: Improve error here to include the names of some clusters using the clusterClass.
		return field.Forbidden(field.NewPath("clusterClass"),
			fmt.Sprintf("clusterClass %v can not be deleted as it is still in use by some clusters", clusterClass.Name))
	}
	return nil
}

func (webhook *ClusterClass) validate(old, new *clusterv1.ClusterClass) error {
	// NOTE: ClusterClass and managed topologies are behind ClusterTopology feature gate flag; the web hook
	// must prevent creating new objects new case the feature flag is disabled.
	if !feature.Gates.Enabled(feature.ClusterTopology) {
		return field.Forbidden(
			field.NewPath("spec"),
			"can be set only if the ClusterTopology feature flag is enabled",
		)
	}

	var allErrs field.ErrorList

	// Ensure all references are valid.
	allErrs = append(allErrs, check.ClusterClassReferencesAreValid(new)...)

	// Ensure all MachineDeployment classes are unique.
	allErrs = append(allErrs, check.MachineDeploymentClassesAreUnique(new)...)

	// Ensure spec changes are compatible.
	allErrs = append(allErrs, check.ClusterClassesAreCompatible(old, new)...)

	// Ensure no MachineDeploymentClass currently in use has been removed from the ClusterClass.
	allErrs = append(allErrs, webhook.validateLivingMachineDeploymentClassesNotRemoved(old, new)...)

	// Validate variables.
	allErrs = append(allErrs, variables.ValidateClusterClassVariables(new.Spec.Variables, field.NewPath("spec", "variables"))...)

	if len(allErrs) > 0 {
		return apierrors.NewInvalid(clusterv1.GroupVersion.WithKind("ClusterClass").GroupKind(), new.Name, allErrs)
	}
	return nil
}

func (webhook *ClusterClass) validateLivingMachineDeploymentClassesNotRemoved(old, new *clusterv1.ClusterClass) field.ErrorList {
	var allErrs field.ErrorList
	if old == nil {
		return allErrs
	}
	missingClasses := webhook.removedMachineClasses(old, new)

	// If no classes have been removed return early as no further checks are needed.
	if len(missingClasses) == 0 {
		return allErrs
	}

	// TODO: The first part of this method will need to be broken out to properly account for changes when also validating variables.
	// Retrieve all clusters using the ClusterClass.
	clusters, errs := webhook.clustersUsingClusterClass(old)
	if len(errs) > 0 {
		allErrs = append(allErrs, errs...)
		return allErrs
	}

	// Retrieve all the MachineDeployments in the clusterClass namespace using a managed topology.
	machineDeployments, errs := webhook.topologyManagedMachineDeployments(new.Namespace)
	if len(errs) > 0 {
		allErrs = append(allErrs, errs...)
		return allErrs
	}

	// Create a set of records of machineDeploymentClass information where the name is a fully qualified name for the
	// MachineDeployment topology in the form cluster.Name/machineDeploymentTopology.Name and links to the machineDeploymentClass Name.
	mdcRecords := map[string]string{}
	for _, c := range clusters {
		for _, machineDeploymentClass := range c.Spec.Topology.Workers.MachineDeployments {
			if missingClasses.Has(machineDeploymentClass.Class) {
				mdcRecords[fmt.Sprintf("%v/%v", c.Name, machineDeploymentClass.Name)] = machineDeploymentClass.Class
			}
		}
	}

	// For each machineDeployment using a managed topology return an error if it is using a class that has been removed in the ClusterClass change.
	for _, md := range machineDeployments {
		mdName := fmt.Sprintf("%v/%v", md.Labels[clusterv1.ClusterLabelName], md.Labels[clusterv1.ClusterTopologyMachineDeploymentLabelName])
		if _, ok := mdcRecords[mdName]; ok {
			allErrs = append(allErrs, field.Forbidden(field.NewPath(""), fmt.Sprintf("MachineDeploymentClass %v is in use in MachineDeployment %v in Cluster %v. ClusterClass %v modification not allowed",
				mdcRecords[mdName], md.Labels[clusterv1.ClusterTopologyMachineDeploymentLabelName], md.Labels[clusterv1.ClusterLabelName], old.Name),
			))
		}
	}

	return allErrs
}

func (webhook *ClusterClass) topologyManagedMachineDeployments(namespace string) ([]clusterv1.MachineDeployment, field.ErrorList) {
	var allErrs field.ErrorList

	// List all the MachineDeployments in the current cluster using a managed topology.
	machineDeployments := &clusterv1.MachineDeploymentList{}
	err := webhook.Client.List(context.Background(), machineDeployments,
		client.MatchingLabels{
			clusterv1.ClusterTopologyOwnedLabel: "",
		},
		// TODO: Should this be namespaced in future?
		client.InNamespace(namespace),
	)
	if err != nil {
		allErrs = append(allErrs, field.InternalError(field.NewPath(""), err))
		return nil, allErrs
	}
	return machineDeployments.Items, nil
}

func (webhook *ClusterClass) removedMachineClasses(old, new *clusterv1.ClusterClass) sets.String {
	missingClasses := sets.NewString()

	// Ensure no MachineDeploymentClass in use has been removed.
	classes := webhook.classNamesFromWorkerClass(new.Spec.Workers)
	for _, oldClass := range old.Spec.Workers.MachineDeployments {
		if !classes.Has(oldClass.Class) {
			missingClasses.Insert(oldClass.Class)
		}
	}
	return missingClasses
}

// classNames returns the set of MachineDeployment class names.
func (webhook *ClusterClass) classNamesFromWorkerClass(w clusterv1.WorkersClass) sets.String {
	classes := sets.NewString()
	for _, class := range w.MachineDeployments {
		classes.Insert(class.Class)
	}
	return classes
}

func (webhook *ClusterClass) clustersUsingClusterClass(clusterClass *clusterv1.ClusterClass) ([]clusterv1.Cluster, field.ErrorList) {
	var allErrs field.ErrorList
	clusters := &clusterv1.ClusterList{}
	clustersUsingClusterClass := []clusterv1.Cluster{}
	err := webhook.Client.List(context.Background(), clusters,
		client.MatchingLabels{
			clusterv1.ClusterTopologyOwnedLabel: "",
		},
		//TODO: This will be an issue when doing clusterClass across namespaces.
		client.InNamespace(clusterClass.Namespace),
	)
	if err != nil {
		allErrs = append(allErrs, field.InternalError(field.NewPath(""), err))
		return nil, allErrs
	}
	for _, c := range clusters.Items {
		if c.Spec.Topology.Class == clusterClass.Name {
			clustersUsingClusterClass = append(clustersUsingClusterClass, c)
		}
	}
	return clustersUsingClusterClass, nil
}
