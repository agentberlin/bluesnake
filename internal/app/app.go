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

import (
	"context"
	"sync"

	"github.com/agentberlin/bluesnake/internal/store"
)

// App represents the core application logic
type App struct {
	ctx          context.Context
	store        *store.Store
	emitter      EventEmitter
	activeCrawls map[uint]*activeCrawl
	crawlsMutex  sync.RWMutex
}

// NewApp creates a new App instance with dependencies injected
func NewApp(st *store.Store, emitter EventEmitter) *App {
	if emitter == nil {
		emitter = &NoOpEmitter{}
	}

	return &App{
		store:        st,
		emitter:      emitter,
		activeCrawls: make(map[uint]*activeCrawl),
	}
}

// Startup initializes the app with a context
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}
