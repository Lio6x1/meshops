package main

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// These checks guard the actual deployable configuration: an accidental ports
// entry must not turn the explicit plaintext demo mode into an exposed API.
func TestDemoComposePublishesOnlyLocalWebAndKeepsSeparateProject(t *testing.T) {
	raw, err := os.ReadFile("../../compose.demo.yml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Name     string `yaml:"name"`
		Services map[string]struct {
			User        string   `yaml:"user"`
			Ports       []string `yaml:"ports"`
			NetworkMode string   `yaml:"network_mode"`
			Volumes     []string `yaml:"volumes"`
			Networks    []string `yaml:"networks"`
		} `yaml:"services"`
		Networks map[string]struct {
			Internal bool `yaml:"internal"`
		} `yaml:"networks"`
	}
	if err = yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if config.Name != "meshops-demo" || !config.Networks["private"].Internal {
		t.Fatal("demo isolation changed")
	}
	if frontend, exists := config.Networks["frontend"]; !exists || frontend.Internal {
		t.Fatal("web requires a separate frontend bridge for its published loopback port")
	}
	if config.Services["gateway"].User != "10002:10001" || config.Services["init"].User != "0:0" {
		t.Fatal("gateway/init credential identities changed")
	}
	ports := 0
	for name, service := range config.Services {
		if service.NetworkMode != "" {
			t.Errorf("%s overrides network mode", name)
		}
		if name == "web" {
			// Only nginx joins both bridges. All application and storage
			// containers remain on the internal network without host ports.
			if len(service.Networks) != 2 || !((service.Networks[0] == "frontend" && service.Networks[1] == "private") || (service.Networks[0] == "private" && service.Networks[1] == "frontend")) {
				t.Error("web must join exactly the frontend and private networks")
			}
		} else if len(service.Networks) != 1 || service.Networks[0] != "private" {
			t.Errorf("%s not isolated", name)
		}
		for _, port := range service.Ports {
			ports++
			if name != "web" || port != "127.0.0.1:18090:8080" {
				t.Errorf("unexpected published port %s %s", name, port)
			}
		}
		for _, volume := range service.Volumes {
			if strings.Contains(volume, "docker.sock") || strings.HasPrefix(volume, "/") || strings.HasPrefix(volume, ".") {
				t.Errorf("unexpected host mount %s %s", name, volume)
			}
		}
	}
	if ports != 1 {
		t.Fatal("expected one web entrypoint")
	}
}
