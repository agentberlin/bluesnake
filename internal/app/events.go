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

package app

// EventType represents the type of event
type EventType string

const (
	EventCrawlStarted   EventType = "crawl:started"
	EventCrawlCompleted EventType = "crawl:completed"
	EventCrawlStopped   EventType = "crawl:stopped"
)

// EventEmitter is the interface for emitting events
// Each transport layer implements this differently:
// - Desktop (Wails): runtime.EventsEmit
// - HTTP: WebSocket/SSE broadcasting
// - MCP: MCP notifications
type EventEmitter interface {
	Emit(eventType EventType, data interface{})
}

// NoOpEmitter is a default implementation that does nothing
// Useful for testing or when events aren't needed
type NoOpEmitter struct{}

// Emit does nothing
func (n *NoOpEmitter) Emit(eventType EventType, data interface{}) {
	// Do nothing
}
