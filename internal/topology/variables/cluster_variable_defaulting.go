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

package variables

import (
	"encoding/json"
	"fmt"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	structuraldefaulting "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// DefaultClusterVariables defaults variables which do not exist in clusterVariable, if they
// have a default value in the corresponding schema in clusterClassVariable.
func DefaultClusterVariables(clusterVariables []clusterv1.ClusterVariable, clusterClassVariables []clusterv1.ClusterClassVariable, fldPath *field.Path) ([]clusterv1.ClusterVariable, field.ErrorList) {
	var allErrs field.ErrorList

	// Build maps for easier and faster access.
	clusterVariablesMap := map[string]*clusterv1.ClusterVariable{}
	for i := range clusterVariables {
		clusterVariablesMap[clusterVariables[i].Name] = &clusterVariables[i]
	}
	clusterClassVariablesMap := map[string]*clusterv1.ClusterClassVariable{}
	for i := range clusterClassVariables {
		clusterClassVariablesMap[clusterClassVariables[i].Name] = &clusterClassVariables[i]
	}

	// Loop through variables in the ClusterClass and default variables if:
	// * the variable does not exist in the Cluster.
	// * the schema has a default value in the ClusterClass.
	defaultedClusterVariables := []clusterv1.ClusterVariable{}
	for variableName, clusterClassVariable := range clusterClassVariablesMap {
		// Don't default if the variable already exists, use
		// the variable from the Cluster instead.
		if clusterVariable, ok := clusterVariablesMap[variableName]; ok {
			defaultedClusterVariables = append(defaultedClusterVariables, *clusterVariable)
			continue
		}

		// Don't default if there is no default value in the schema.
		// NOTE: In this case the variable won't be added to the Cluster.
		if clusterClassVariable.Schema.OpenAPIV3Schema.Default == nil {
			continue
		}

		// Create a new clusterVariable and default it.
		clusterVariable := &clusterv1.ClusterVariable{
			Name: variableName,
		}
		if errs := defaultClusterVariable(clusterVariable, clusterClassVariable, fldPath); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
			continue
		}

		defaultedClusterVariables = append(defaultedClusterVariables, *clusterVariable)
	}

	return defaultedClusterVariables, nil
}

// defaultClusterVariable defaults a clusterVariable based on the default value in the clusterClassVariable.
func defaultClusterVariable(clusterVariable *clusterv1.ClusterVariable, clusterClassVariable *clusterv1.ClusterClassVariable, fldPath *field.Path) field.ErrorList {
	// Convert schema to upstream schema.
	upstreamSchema, err := convertToUpstreamJSONSchemaProps(&clusterClassVariable.Schema.OpenAPIV3Schema, fldPath)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, "",
			fmt.Sprintf("invalid schema in ClusterClass for variable %q: error to convert schema %v", clusterVariable.Name, err))}
	}

	// Structural schema defaulting does not work with scalar values,
	// so we wrap the schema and the variable in objects.
	// type: object
	// properties:
	//   <variable-name>: <variable-schema>
	wrappedSchema := &apiextensions.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensions.JSONSchemaProps{
			clusterVariable.Name: *upstreamSchema,
		},
	}
	var value interface{}
	wrappedVariable := map[string]interface{}{
		clusterVariable.Name: value,
	}

	// Default the variable via the structural schema library.
	ss, err := structuralschema.NewStructural(wrappedSchema)
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, "",
			fmt.Sprintf("failed defaulting variable %q: %v", clusterVariable.Name, err))}
	}
	structuraldefaulting.Default(wrappedVariable, ss)

	// Marshal the defaulted value.
	defaultedVariableValue, err := json.Marshal(wrappedVariable[clusterVariable.Name])
	if err != nil {
		return field.ErrorList{field.Invalid(fldPath, "",
			fmt.Sprintf("failed to marshal default value of variable %q: %v", clusterVariable.Name, err))}
	}

	clusterVariable.Value = apiextensionsv1.JSON{
		Raw: defaultedVariableValue,
	}
	return nil
}
