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

package adapter

import (
	"errors"

	"github.com/sirupsen/logrus"
)

type Job func() error

type Worker struct {
	jobs chan Job
}

// NewWorker starts a worker queue with a specified maximum worker
// number for concurrent and limited job execution.
func NewWorker(count int) (*Worker, error) {
	if count <= 0 {
		return nil, errors.New("worker count should be greater than 0")
	}

	worker := Worker{
		jobs: make(chan Job, count),
	}

	for i := 0; i <= count; i++ {
		go func() {
			for {
				job := <-worker.jobs
				if err := job(); err != nil {
					logrus.Errorf("convert in worker: %s", err)
				}
			}
		}()
	}

	return &worker, nil
}

func (worker *Worker) Dispatch(job func() error) {
	go func() {
		worker.jobs <- job
	}()
}
