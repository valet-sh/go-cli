// Copyright 2026 TechDivision GmbH
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

// Package config parses .valet-sh.yml project files and the global
// ~/.valet-sh/config.yml. The YAML structure is kept 100% identical to the
// existing format so that no user-facing files need to change.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const ProjectFileName = ".valet-sh.yml"

// ProjectConfig is the Go representation of .valet-sh.yml.
// Every field uses the same YAML key as the existing Ansible-consumed file.
type ProjectConfig struct {
	Hub      HubConfig      `yaml:"hub"`
	Services ServicesConfig `yaml:"services"`
	Instance InstanceConfig `yaml:"instance"`
	Template TemplateConfig `yaml:"template,omitempty"`
}

// HubConfig holds remote hub connection details used by valet-restore.
type HubConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	Path string `yaml:"path"`
}

// ServicesConfig declares which service versions the project requires.
type ServicesConfig struct {
	Composer      *ComposerService      `yaml:"composer,omitempty"`
	Node          *NodeService          `yaml:"node,omitempty"`
	PHP           *PHPService           `yaml:"php,omitempty"`
	MySQL         *MySQLService         `yaml:"mysql,omitempty"`
	MariaDB       *MariaDBService       `yaml:"mariadb,omitempty"`
	Elasticsearch *ElasticsearchService `yaml:"elasticsearch,omitempty"`
	OpenSearch    *OpenSearchService    `yaml:"opensearch,omitempty"`
	RabbitMQ      *RabbitMQService      `yaml:"rabbitmq,omitempty"`
	Redis         *RedisService         `yaml:"redis,omitempty"`
	Valkey        *ValkeyService        `yaml:"valkey,omitempty"`
}

type ComposerService struct {
	Version int `yaml:"version"`
}

type NodeService struct {
	Version any `yaml:"version"` // string or int both accepted
}

type PHPService struct {
	Version any `yaml:"version"` // e.g. 8.1 or "8.1"
}

type MySQLService struct {
	Version  any    `yaml:"version"`
	Database string `yaml:"database,omitempty"`
}

type MariaDBService struct {
	Version  any    `yaml:"version"`
	Database string `yaml:"database,omitempty"`
}

type ElasticsearchService struct {
	Version any      `yaml:"version"`
	Plugins []string `yaml:"plugins,omitempty"`
}

type OpenSearchService struct {
	Version any      `yaml:"version"`
	Plugins []string `yaml:"plugins,omitempty"`
}

type RabbitMQService struct {
	Vhost string `yaml:"vhost,omitempty"`
}

type RedisService struct {
	Version any `yaml:"version,omitempty"`
}

type ValkeyService struct {
	Version any `yaml:"version,omitempty"`
}

// InstanceConfig contains project-specific bootstrap configuration.
type InstanceConfig struct {
	Key             string            `yaml:"key"`
	Type            string            `yaml:"type"`
	Path            string            `yaml:"path,omitempty"`
	Multidomain     map[string]string `yaml:"multidomain,omitempty"`
	Sync            *SyncConfig       `yaml:"sync,omitempty"`
	CryptKey        string            `yaml:"crypt_key,omitempty"`
	Crypt           *CryptConfig      `yaml:"crypt,omitempty"`
	Config          map[string]any    `yaml:"config,omitempty"`
	Session         map[string]any    `yaml:"session,omitempty"`
	Cache           map[string]any    `yaml:"cache,omitempty"`
	ProcessedConfig string            `yaml:"processed_config,omitempty"`
}

type SyncConfig struct {
	Identifier         string             `yaml:"identifier,omitempty"`
	DB                 bool               `yaml:"db"`
	FS                 []string           `yaml:"fs,omitempty"`
	PostRestoreActions *PostRestoreConfig `yaml:"post_restore_actions,omitempty"`
}

type PostRestoreConfig struct {
	Indexer  *IndexerConfig `yaml:"indexer,omitempty"`
	Commands []string       `yaml:"commands,omitempty"`
}

type IndexerConfig struct {
	ReindexAll bool `yaml:"reindexAll"`
}

type CryptConfig struct {
	Key     string `yaml:"key,omitempty"`
	JWKSKey string `yaml:"jwks_key,omitempty"`
}

// TemplateConfig allows specifying a custom env.php template path.
type TemplateConfig struct {
	Path string `yaml:"path,omitempty"`
}

// LoadProject reads and parses the .valet-sh.yml file from the given directory.
// Returns a descriptive error if the file is missing or malformed.
func LoadProject(dir string) (*ProjectConfig, error) {
	path := filepath.Join(dir, ProjectFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no %s found in %s", ProjectFileName, dir)
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return &cfg, nil
}

// Validate performs basic sanity checks on the parsed config and returns a
// slice of human-readable error strings (empty means valid).
func (c *ProjectConfig) Validate() []string {
	var errs []string

	if c.Instance.Key == "" {
		errs = append(errs, "instance.key is required")
	}
	if c.Instance.Type == "" {
		errs = append(errs, "instance.type is required")
	}

	validTypes := map[string]bool{
		"magento2": true,
		"magento1": true,
		"neos":     true,
		"aem":      true,
		"orocrm":   true,
	}
	if c.Instance.Type != "" && !validTypes[c.Instance.Type] {
		errs = append(errs, fmt.Sprintf("instance.type %q is not supported (valid: magento2, magento1, neos, aem, orocrm)", c.Instance.Type))
	}

	if c.Services.MySQL != nil && c.Services.MariaDB != nil {
		errs = append(errs, "services.mysql and services.mariadb cannot both be set")
	}
	if c.Services.Elasticsearch != nil && c.Services.OpenSearch != nil {
		errs = append(errs, "services.elasticsearch and services.opensearch cannot both be set")
	}

	return errs
}
