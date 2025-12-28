/*
Copyright 2022 The Flux authors

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

package repository

// ReferenceError indicate invalid chart reference.
type ReferenceError struct {
	Err error
}

// Error implements the error interface.
func (er *ReferenceError) Error() string {
	return er.Err.Error()
}

// Unwrap returns the underlying error.
func (er *ReferenceError) Unwrap() error {
	return er.Err
}

// ExternalError is a generic error for errors related to external API calls.
type ExternalError struct {
	Err error
}

// Error implements the error interface.
func (ee *ExternalError) Error() string {
	return ee.Err.Error()
}

// Unwrap returns the underlying error.
func (ee *ExternalError) Unwrap() error {
	return ee.Err
}
