// Copyright 2025 Agentic World, LLC (Sherin Thomas)
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

package types

// ServerInfo contains information about a successfully started server
type ServerInfo struct {
	PublicURL string `json:"publicURL"`
	Port      int    `json:"port"`
}

// ServerStatus represents the current status of the server and tunnel
type ServerStatus struct {
	IsRunning bool   `json:"isRunning"`
	PublicURL string `json:"publicURL"`
	Port      int    `json:"port"`
	Error     string `json:"error,omitempty"`
}
