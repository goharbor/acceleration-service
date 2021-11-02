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

package task

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

const taskMaximumKeepPeriod = time.Hour * 24

const StatusProcessing = "PROCESSING"
const StatusCompleted = "COMPLETED"
const StatusFailed = "FAILED"

type Task struct {
	ID       string     `json:"id"`
	Created  time.Time  `json:"created"`
	Finished *time.Time `json:"finished"`
	Source   string     `json:"source"`
	Status   string     `json:"status"`
	Reason   string     `json:"reason"`
}

type manager struct {
	mutex sync.Mutex
	tasks map[string]*Task
}

var Manager *manager

func init() {
	Manager = &manager{
		mutex: sync.Mutex{},
		tasks: make(map[string]*Task),
	}
}

func (t *Task) IsExpired() bool {
	if t.Status != StatusProcessing &&
		time.Now().After(t.Finished.Add(taskMaximumKeepPeriod)) {
		return true
	}
	return false
}

func (m *manager) Create(source string) string {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	id := uuid.NewString()

	m.tasks[id] = &Task{
		ID:       id,
		Created:  time.Now(),
		Finished: nil,
		Source:   source,
		Status:   StatusProcessing,
		Reason:   "",
	}

	return id
}

func (m *manager) Finish(id string, err error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Update task status.
	task := m.tasks[id]
	if task != nil {
		if err != nil {
			task.Status = StatusFailed
			task.Reason = err.Error()
		} else {
			task.Status = StatusCompleted
		}
		now := time.Now()
		task.Finished = &now
	}

	// Evict expired tasks.
	for id, task := range m.tasks {
		if task.IsExpired() {
			delete(m.tasks, id)
		}
	}
}

func (m *manager) List() []*Task {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	tasks := make([]*Task, 0)
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Created.After(tasks[j].Created)
	})

	return tasks
}
