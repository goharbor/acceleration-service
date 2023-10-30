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
	"encoding/json"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/goharbor/acceleration-service/pkg/converter"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	bolt "go.etcd.io/bbolt"
)

var bucketObjectTasks = []byte("tasks")

const taskMaximumKeepPeriod = time.Hour * 24

const StatusProcessing = "PROCESSING"
const StatusCompleted = "COMPLETED"
const StatusFailed = "FAILED"

type Task struct {
	ID         string    `json:"id"`
	Created    time.Time `json:"created"`
	Finished   time.Time `json:"finished"`
	Source     string    `json:"source"`
	SourceSize uint      `json:"source_size"`
	TargetSize uint      `json:"target_size"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason"`
}

type manager struct {
	mutex sync.Mutex
	db    *bolt.DB
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

// Init manager supported by boltdb.
func (m *manager) Init(workDir string) error {
	bdb, err := bolt.Open(filepath.Join(workDir, "task.db"), 0655, nil)
	if err != nil {
		return errors.Wrap(err, "create task database")
	}
	m.db = bdb
	return m.initDatabase()
}

// initDatabase loads tasks from the database into memory.
func (m *manager) initDatabase() error {
	return m.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("tasks"))
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(k, v []byte) error {
			var task Task
			if err := json.Unmarshal(v, &task); err != nil {
				return err
			}
			if task.Status == StatusProcessing {
				return bucket.Delete([]byte(task.ID))
			}
			m.tasks[task.ID] = &task
			return nil
		})
	})
}

// updateBucket updates task in bucket and creates a new bucket if it doesn't already exist.
func (m *manager) updateBucket(task *Task) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(bucketObjectTasks)
		if err != nil {
			return err
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			return err
		}

		return bucket.Put([]byte(task.ID), taskJSON)
	})
}

// deleteBucket deletes a task in bucket
func (m *manager) deleteBucket(taskID string) error {
	return m.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketObjectTasks)
		if bucket == nil {
			return nil
		}
		return bucket.Delete([]byte(taskID))
	})
}

// Create new task
func (m *manager) Create(source string) (string, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	id := uuid.NewString()

	task := &Task{
		ID:         id,
		Created:    time.Now(),
		Source:     source,
		SourceSize: 0,
		TargetSize: 0,
		Status:     StatusProcessing,
		Reason:     "",
	}
	m.tasks[id] = task
	if err := m.updateBucket(task); err != nil {
		return "", err
	}
	m.tasks[id] = task
	return id, nil
}

// Finish a task
func (m *manager) Finish(id string, metric *converter.Metric, err error) error {
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
		if metric != nil {
			task.SourceSize = uint(metric.SourceImageSize)
			task.TargetSize = uint(metric.TargetImageSize)
		}
		task.Finished = time.Now()
	}
	if err := m.updateBucket(task); err != nil {
		return err
	}

	// Evict expired tasks.
	for id, task := range m.tasks {
		if task.IsExpired() {
			delete(m.tasks, id)
			if err := m.deleteBucket(id); err != nil {
				return err
			}
		}
	}
	return nil
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
