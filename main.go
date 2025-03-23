package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/rpc"
	"os"
	"strings"
	"time"

	"github.com/apenella/go-ansible/v2/pkg/execute"
	results "github.com/apenella/go-ansible/v2/pkg/execute/result/json"
	"github.com/apenella/go-ansible/v2/pkg/execute/stdoutcallback"
	"github.com/apenella/go-ansible/v2/pkg/playbook"

	"github.com/v1Flows/runner/pkg/executions"
	"github.com/v1Flows/runner/pkg/plugins"
	"github.com/v1Flows/shared-library/pkg/models"

	"github.com/hashicorp/go-plugin"
)

// Plugin is an implementation of the Plugin interface
type Plugin struct{}

func (p *Plugin) ExecuteTask(request plugins.ExecuteTaskRequest) (plugins.Response, error) {
	play := ""
	inventory := ""
	become := false
	limit := ""
	check := false
	diff := false
	user := ""
	password := ""
	becomeUser := ""
	becomePass := ""

	// access action params
	for _, param := range request.Step.Action.Params {
		if param.Key == "playbook" {
			if strings.Contains(param.Value, "/") {
				play = request.Workspace + "/" + param.Value
			} else {
				play = param.Value
			}
		}
		if param.Key == "inventory" {
			// if inventory is a path prefix with workspace
			if strings.Contains(param.Value, "/") {
				inventory = request.Workspace + "/" + param.Value
			} else {
				inventory = param.Value
			}
		}
		if param.Key == "become" {
			become = param.Value == "true"
		}
		if param.Key == "limit" {
			limit = param.Value
		}
		if param.Key == "check" {
			check = param.Value == "true"
		}
		if param.Key == "diff" {
			diff = param.Value == "true"
		}
		if param.Key == "user" {
			user = param.Value
		}
		if param.Key == "password" {
			password = param.Value
		}
		if param.Key == "become_user" {
			becomeUser = param.Value
		}
		if param.Key == "become_pass" {
			becomePass = param.Value
		}
	}

	// check if playbook file exists
	if _, err := os.Stat(play); errors.Is(err, os.ErrNotExist) {
		err = executions.UpdateStep(request.Config, request.Execution.ID.String(), models.ExecutionSteps{
			ID: request.Step.ID,
			Messages: []models.Message{
				{
					Title: "Ansible Playbook",
					Lines: []string{
						"Playbook file does not exist",
						err.Error(),
					},
				},
			},
			Status:     "error",
			StartedAt:  time.Now(),
			FinishedAt: time.Now(),
		}, request.Platform)
		if err != nil {
			return plugins.Response{
				Success: false,
			}, err
		}
		return plugins.Response{
			Success: false,
		}, errors.New("playbook file does not exist")
	}

	// if inventory is a file check and not a comma separated list check if file exists
	if !strings.Contains(inventory, ",") {
		if _, err := os.Stat(inventory); errors.Is(err, os.ErrNotExist) {
			err = executions.UpdateStep(request.Config, request.Execution.ID.String(), models.ExecutionSteps{
				ID: request.Step.ID,
				Messages: []models.Message{
					{
						Title: "Ansible Playbook",
						Lines: []string{
							"Inventory file does not exist",
							err.Error(),
						},
					},
				},
				Status:     "error",
				StartedAt:  time.Now(),
				FinishedAt: time.Now(),
			}, request.Platform)
			if err != nil {
				return plugins.Response{
					Success: false,
				}, err
			}
			return plugins.Response{
				Success: false,
			}, errors.New("inventory file does not exist")
		}
	}

	err := executions.UpdateStep(request.Config, request.Execution.ID.String(), models.ExecutionSteps{
		ID: request.Step.ID,
		Messages: []models.Message{
			{
				Title: "Ansible Playbook",
				Lines: []string{
					"Starting Ansible Playbook",
					"Playbook: " + play,
					"Inventory: " + inventory,
				},
			},
		},
		Status:    "running",
		StartedAt: time.Now(),
	}, request.Platform)
	if err != nil {
		return plugins.Response{
			Success: false,
		}, err
	}

	var res *results.AnsiblePlaybookJSONResults
	buff := new(bytes.Buffer)

	ansiblePlaybookOptions := &playbook.AnsiblePlaybookOptions{
		Connection: "local",
		Inventory:  inventory,
		Become:     become,
		Limit:      limit,
		Check:      check,
		Diff:       diff,
		User:       user,
		BecomeUser: becomeUser,
		ExtraVars: map[string]interface{}{
			"ansible_password":    password,
			"ansible_become_pass": becomePass,
		},
	}

	playbookCmd := playbook.NewAnsiblePlaybookCmd(
		playbook.WithPlaybooks(play),
		playbook.WithPlaybookOptions(ansiblePlaybookOptions),
	)

	exec := stdoutcallback.NewJSONStdoutCallbackExecute(
		execute.NewDefaultExecute(
			execute.WithCmd(playbookCmd),
			execute.WithErrorEnrich(playbook.NewAnsiblePlaybookErrorEnrich()),
			execute.WithWrite(io.Writer(buff)),
		),
	)

	err = exec.Execute(context.TODO())
	if err != nil {
		fmt.Println(err.Error())
	}

	res, err = results.ParseJSONResultsStream(io.Reader(buff))
	if err != nil {
		panic(err)
	}

	msgOutput := struct {
		Host    string `json:"host"`
		Message string `json:"message"`
	}{}

	for _, play := range res.Plays {
		for _, task := range play.Tasks {
			for _, content := range task.Hosts {
				err = json.Unmarshal([]byte(fmt.Sprint(content.Stdout)), &msgOutput)
				if err != nil {
					panic(err)
				}

				_ = executions.UpdateStep(request.Config, request.Execution.ID.String(), models.ExecutionSteps{
					ID: request.Step.ID,
					Messages: []models.Message{
						{
							Title: "Ansible Playbook",
							Lines: []string{
								"Host: " + msgOutput.Host,
								msgOutput.Message,
							},
						},
					},
				}, request.Platform)

				fmt.Printf("[%s] %s\n", msgOutput.Host, msgOutput.Message)
			}
		}
	}

	// update the step with the messages
	err = executions.UpdateStep(request.Config, request.Execution.ID.String(), models.ExecutionSteps{
		ID: request.Step.ID,
		Messages: []models.Message{
			{
				Title: "Ansible Playbook",
				Lines: []string{
					"Ansible Playbook executed successfully",
				},
			},
		},
		Status:     "success",
		StartedAt:  time.Now(),
		FinishedAt: time.Now(),
	}, request.Platform)
	if err != nil {
		return plugins.Response{
			Success: false,
		}, err
	}

	return plugins.Response{
		Success: true,
	}, nil
}

func (p *Plugin) EndpointRequest(request plugins.EndpointRequest) (plugins.Response, error) {
	return plugins.Response{
		Success: false,
	}, errors.New("not implemented")
}

func (p *Plugin) Info(request plugins.InfoRequest) (models.Plugin, error) {
	var plugin = models.Plugin{
		Name:    "Ansible",
		Type:    "action",
		Version: "1.0.0",
		Author:  "JustNZ",
		Action: models.Action{
			Name:        "Ansible",
			Description: "Execute Ansible Playbook",
			Plugin:      "ansible",
			Icon:        "mdi:ansible",
			Category:    "Automation",
			Params: []models.Params{
				{
					Key:         "playbook",
					Title:       "Playbook",
					Category:    "General",
					Type:        "text",
					Default:     "",
					Required:    true,
					Description: "Path to the playbook file",
				},
				{
					Key:         "inventory",
					Title:       "Inventory",
					Category:    "General",
					Type:        "text",
					Default:     "",
					Required:    true,
					Description: "Path to the inventory file or comma separated host list",
				},
				{
					Key:         "user",
					Title:       "User",
					Category:    "Authentication",
					Type:        "text",
					Default:     "",
					Required:    false,
					Description: "Connect as this user",
				},
				{
					Key:         "password",
					Title:       "Password",
					Category:    "Authentication",
					Type:        "password",
					Default:     "",
					Required:    false,
					Description: "Connection user password",
				},
				{
					Key:         "limit",
					Title:       "Limit",
					Category:    "General",
					Type:        "text",
					Default:     "",
					Required:    false,
					Description: "Further limit selected hosts to an additional pattern",
				},
				{
					Key:         "become",
					Title:       "Become",
					Category:    "Sudo",
					Type:        "boolean",
					Default:     "false",
					Required:    false,
					Description: "Run playbook with become",
				},
				{
					Key:         "become_user",
					Title:       "Become User",
					Category:    "Sudo",
					Type:        "text",
					Default:     "root",
					Required:    false,
					Description: "User to run become tasks with",
				},
				{
					Key:         "become_pass",
					Title:       "Become Password",
					Category:    "Sudo",
					Type:        "password",
					Default:     "",
					Required:    false,
					Description: "Become user password",
				},
				{
					Key:         "check",
					Title:       "Check",
					Category:    "Utility",
					Type:        "boolean",
					Default:     "false",
					Required:    false,
					Description: "Don't make any changes; instead, try to predict some of the changes that may occur",
				},
				{
					Key:         "diff",
					Title:       "Diff",
					Category:    "Utility",
					Type:        "boolean",
					Default:     "false",
					Required:    false,
					Description: "When changing (small) files and templates, show the differences in those files",
				},
			},
		},
		Endpoint: models.Endpoint{},
	}

	return plugin, nil
}

// PluginRPCServer is the RPC server for Plugin
type PluginRPCServer struct {
	Impl plugins.Plugin
}

func (s *PluginRPCServer) ExecuteTask(request plugins.ExecuteTaskRequest, resp *plugins.Response) error {
	result, err := s.Impl.ExecuteTask(request)
	*resp = result
	return err
}

func (s *PluginRPCServer) EndpointRequest(request plugins.EndpointRequest, resp *plugins.Response) error {
	result, err := s.Impl.EndpointRequest(request)
	*resp = result
	return err
}

func (s *PluginRPCServer) Info(request plugins.InfoRequest, resp *models.Plugin) error {
	result, err := s.Impl.Info(request)
	*resp = result
	return err
}

// PluginServer is the implementation of plugin.Plugin interface
type PluginServer struct {
	Impl plugins.Plugin
}

func (p *PluginServer) Server(*plugin.MuxBroker) (interface{}, error) {
	return &PluginRPCServer{Impl: p.Impl}, nil
}

func (p *PluginServer) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &plugins.PluginRPC{Client: c}, nil
}

func main() {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: plugin.HandshakeConfig{
			ProtocolVersion:  1,
			MagicCookieKey:   "PLUGIN_MAGIC_COOKIE",
			MagicCookieValue: "hello",
		},
		Plugins: map[string]plugin.Plugin{
			"plugin": &PluginServer{Impl: &Plugin{}},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
