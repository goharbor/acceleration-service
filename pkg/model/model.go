// Copyright Project Harbor Authors
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

// To avoid importing a large number of redundant dependencies,
// we ported harbor structure definitions to this file.
//
// Ported from github.com/goharbor/harbor/src/controller/event
// Ported from github.com/goharbor/harbor/src/pkg/notifier/model
package model

const TopicPushArtifact = "PUSH_ARTIFACT"

// Payload of notification event
type Payload struct {
	Type      string     `json:"type"`
	EventData *EventData `json:"event_data,omitempty"`
}

// EventData of notification event payload
type EventData struct {
	Resources []*Resource `json:"resources,omitempty"`
}

// Resource describe infos of resource triggered notification
type Resource struct {
	ResourceURL string `json:"resource_url,omitempty"`
}
