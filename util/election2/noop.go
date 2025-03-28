// Copyright 2018 Google LLC. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package election2

import (
	"context"

	"k8s.io/klog/v2"
)

// noopElectionName represents the noop election implementation.
const noopElectionName = "noop"

func init() {
	if err := RegisterProvider(noopElectionName, func() (Factory, error) {
		return NoopFactory{}, nil
	}); err != nil {
		klog.Fatalf("Failed to register %q: %v", noopElectionName, err)
	}
}

// NoopElection is a stub Election that always believes to be the master.
type NoopElection string

// Await returns immediately, as the instance is always the master.
func (ne NoopElection) Await(ctx context.Context) error {
	return nil
}

// WithMastership returns the passed in context as a mastership context.
func (ne NoopElection) WithMastership(ctx context.Context) (context.Context, error) {
	return ctx, nil
}

// Resign does nothing because NoopElection is always the master.
func (ne NoopElection) Resign(ctx context.Context) error {
	return nil
}

// Close does nothing because NoopElection is always the master.
func (ne NoopElection) Close(ctx context.Context) error {
	return nil
}

// NoopFactory creates NoopElection instances.
type NoopFactory struct{}

// NewElection creates a specific NoopElection instance.
func (nf NoopFactory) NewElection(ctx context.Context, resourceID string) (Election, error) {
	return NoopElection(resourceID), nil
}
