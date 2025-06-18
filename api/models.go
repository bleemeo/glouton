// Copyright 2015-2025 Bleemeo
//
// bleemeo.com an infrastructure monitoring solution in the Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

import (
	"net/http"
	"time"
)

type AgentInfo struct {
	RegistrationAt *time.Time `json:"registrationAt,omitempty"`
	LastReport     *time.Time `json:"lastReport,omitempty"`
	IsConnected    bool       `json:"isConnected"`
}

func (a *AgentInfo) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

type AgentStatus struct {
	ServiceName       string  `json:"serviceName"`
	Status            float64 `json:"status"`
	StatusDescription string  `json:"statusDescription"`
}

func (a *AgentStatus) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

type CPUUsage struct {
	User      float64 `json:"User"`
	Nice      float64 `json:"Nice"`
	System    float64 `json:"System"`
	Idle      float64 `json:"Idle"`
	IOWait    float64 `json:"IOWait"`
	Guest     float64 `json:"Guest"`
	GuestNice float64 `json:"GuestNice"`
	Irq       float64 `json:"IRQ"`
	SoftIrq   float64 `json:"SoftIRQ"`
	Steal     float64 `json:"Steal"`
}

type Container struct {
	Command      string     `json:"command"`
	CreatedAt    *time.Time `json:"createdAt,omitempty"`
	ID           string     `json:"id"`
	Image        string     `json:"image"`
	InspectJSON  string     `json:"inspectJSON"`
	Name         string     `json:"name"`
	StartedAt    *time.Time `json:"startedAt,omitempty"`
	State        string     `json:"state"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	IoWriteBytes float64    `json:"ioWriteBytes"`
	IoReadBytes  float64    `json:"ioReadBytes"`
	NetBitsRecv  float64    `json:"netBitsRecv"`
	NetBitsSent  float64    `json:"netBitsSent"`
	MemUsedPerc  float64    `json:"memUsedPerc"`
	CPUUsedPerc  float64    `json:"cpuUsedPerc"`
}

type Containers struct {
	Count        int          `json:"count"`
	CurrentCount int          `json:"currentCount"`
	Containers   []*Container `json:"containers"`
}

func (c *Containers) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (f *Fact) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

type LabelInput struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type MemoryUsage struct {
	Total   float64 `json:"Total"`
	Used    float64 `json:"Used"`
	Free    float64 `json:"Free"`
	Buffers float64 `json:"Buffers"`
	Cached  float64 `json:"Cached"`
}

type MetricInput struct {
	Labels []*LabelInput `json:"labels"`
}

type Pagination struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

type Process struct {
	Pid         int       `json:"pid"`
	Ppid        int       `json:"ppid"`
	CreateTime  time.Time `json:"create_time"`
	Cmdline     string    `json:"cmdline"`
	Name        string    `json:"name"`
	MemoryRss   int64     `json:"memory_rss"`
	CPUPercent  float64   `json:"cpu_percent"`
	CPUTime     float64   `json:"cpu_time"`
	Status      string    `json:"status"`
	Username    string    `json:"username"`
	Executable  string    `json:"executable"`
	ContainerID string    `json:"container_id"`
}

type Service struct {
	Name              string   `json:"name"`
	ContainerID       string   `json:"containerId"`
	IPAddress         string   `json:"ipAddress"`
	ListenAddresses   []string `json:"listenAddresses"`
	ExePath           string   `json:"exePath"`
	Active            bool     `json:"active"`
	Status            float64  `json:"status"`
	StatusDescription *string  `json:"statusDescription,omitempty"`
}

func (s *Service) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

type SwapUsage struct {
	Total float64 `json:"Total"`
	Used  float64 `json:"Used"`
	Free  float64 `json:"Free"`
}

type Tag struct {
	TagName string `json:"tagName"`
}

func (t *Tag) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}

type Topinfo struct {
	Time      time.Time    `json:"Time"`
	Uptime    int          `json:"Uptime"`
	Loads     []float64    `json:"Loads"`
	Users     int          `json:"Users"`
	Processes []*Process   `json:"Processes"`
	CPU       *CPUUsage    `json:"CPU,omitempty"`
	Memory    *MemoryUsage `json:"Memory,omitempty"`
	Swap      *SwapUsage   `json:"Swap,omitempty"`
}

func (t *Topinfo) Render(_ http.ResponseWriter, _ *http.Request) error {
	return nil
}
