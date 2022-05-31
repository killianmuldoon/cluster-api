/*
Copyright 2022 The Kubernetes Authors.

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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime"
)

// ResponseObject is a runtime object extended with methods to handle response-specific fields.
// +kubebuilder:object:generate=false
type ResponseObject interface {
	runtime.Object
	GetMessage() string
	GetStatus() ResponseStatus
	SetMessage(message string)
	SetStatus(status ResponseStatus)
}

// ResponseMeta is the data structure common to all *Response types.
type ResponseMeta struct {
	Message string         `json:"message"`
	Status  ResponseStatus `json:"status"`
}

// SetMessage sets the message field for the ResponseMeta.
func (r *ResponseMeta) SetMessage(message string) {
	r.Message = message
}

// SetStatus sets the status field for the ResponseMeta.
func (r *ResponseMeta) SetStatus(status ResponseStatus) {
	r.Status = status
}

// GetMessage returns the Message field for the ResponseMeta.
func (r *ResponseMeta) GetMessage() string {
	return r.Message
}

// GetStatus returns the Status field for the ResponseMeta.
func (r *ResponseMeta) GetStatus() ResponseStatus {
	return r.Status
}

// ResponseStatus represents the status of the hook response.
// +enum// -
type ResponseStatus string

const (
	// ResponseStatusSuccess represents the success response.
	ResponseStatusSuccess ResponseStatus = "Success"

	// ResponseStatusFailure represents a failure response.
	ResponseStatusFailure ResponseStatus = "Failure"
)
