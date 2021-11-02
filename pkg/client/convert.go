package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/goharbor/acceleration-service/pkg/task"
	"github.com/goharbor/harbor/src/controller/event"
	"github.com/goharbor/harbor/src/pkg/notifier/model"
	"github.com/pkg/errors"
)

func (client *Client) CreateTask(src string, sync bool) error {
	payload := model.Payload{
		Type: event.TopicPushArtifact,
		EventData: &model.EventData{
			Resources: []*model.Resource{
				{
					ResourceURL: src,
				},
			},
		},
	}

	data, err := marshal(payload)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/conversions?sync=%s", strconv.FormatBool(sync))
	resp, err := client.Request(http.MethodPost, path, data, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func (client *Client) ListTask() ([]task.Task, error) {
	resp, err := client.Request(http.MethodGet, "/api/v1/conversions", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tasks []task.Task
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&tasks); err != nil {
		return nil, errors.Wrap(err, "decode response")
	}

	return tasks, nil
}
