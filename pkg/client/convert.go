package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/goharbor/acceleration-service/pkg/model"
	"github.com/goharbor/acceleration-service/pkg/task"
	"github.com/pkg/errors"
)

func (client *Client) CreateTask(src string, sync bool) (*model.CreateTaskResponse, error) {
	payload := model.Payload{
		Type: model.TopicPushArtifact,
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
		return nil, err
	}

	path := fmt.Sprintf("/api/v1/conversions?sync=%s", strconv.FormatBool(sync))
	resp, err := client.Request(http.MethodPost, path, data, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "read response body")
	}

	// Support both new server (JSON object response) and old server ("Ok" string response).
	if strings.TrimSpace(string(body)) == "Ok" {
		return &model.CreateTaskResponse{}, nil
	}

	var result model.CreateTaskResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, errors.Wrap(err, "unmarshal response body")
	}

	return &result, nil
}

func (client *Client) GetTask(id string) (*task.Task, error) {
	path := fmt.Sprintf("/api/v1/conversions/%s", id)
	resp, err := client.Request(http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var t task.Task
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&t); err != nil {
		return nil, errors.Wrap(err, "decode response")
	}

	return &t, nil
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
