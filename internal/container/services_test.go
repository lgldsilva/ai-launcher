package container

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestServiceCatalogHasUniqueIDsAndAllFields(t *testing.T) {
	t.Helper()
	seen := make(map[string]bool, len(Services))
	wantIDs := []string{
		"postgres", "mysql", "mariadb", "oracle", "sqlserver",
		"mongo", "cassandra", "neo4j", "dynamodb",
		"qdrant", "weaviate", "milvus", "chroma", "pgvector",
		"influxdb", "timescaledb", "clickhouse",
		"elasticsearch", "opensearch", "meilisearch", "typesense",
		"kafka", "redpanda", "rabbitmq", "nats", "activemq", "pulsar",
		"redis", "valkey", "dragonfly",
		"minio", "localstack",
		"prometheus", "grafana", "jaeger", "loki", "otel-collector",
		"keycloak", "authentik",
		"airflow", "temporal", "n8n",
		"mailpit", "wiremock", "code-server", "nginx", "traefik", "caddy",
	}
	if len(Services) != len(wantIDs) {
		t.Fatalf("service catalog has %d entries; want %d", len(Services), len(wantIDs))
	}
	for i, service := range Services {
		if service.ID != wantIDs[i] {
			t.Fatalf("service %d has ID %q; want %q", i, service.ID, wantIDs[i])
		}
		if seen[service.ID] {
			t.Fatalf("duplicate service ID %q", service.ID)
		}
		seen[service.ID] = true
		if service.ID == "" || service.Name == "" || service.Category == "" || service.Image == "" {
			t.Fatalf("service has incomplete identity: %#v", service)
		}
		if strings.Contains(service.Image, ":latest") {
			t.Fatalf("service %q uses floating latest image tag: %q", service.ID, service.Image)
		}
		for _, mapping := range service.Ports {
			if mapping.Internal <= 0 || mapping.HostPort() <= 0 {
				t.Fatalf("service %q has invalid port mapping: %#v", service.ID, mapping)
			}
		}
	}
}

func TestServiceCatalogCoversExpectedCategories(t *testing.T) {
	want := []ServiceCategory{
		CategoryDatabaseSQL, CategoryDatabaseNoSQL, CategoryDatabaseVector,
		CategoryDatabaseTS, CategorySearch, CategoryQueue, CategoryCache, CategoryStorage,
		CategoryMonitoring, CategoryAuth, CategoryWorkflow, CategoryDevTools,
	}
	got := ServiceCategories()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ServiceCategories() = %#v; want %#v", got, want)
	}
}

func TestServiceAccessorsAndValidation(t *testing.T) {
	got, ok := ServiceByID(" mongo ")
	if !ok || got.ID != "mongo" {
		t.Fatalf("ServiceByID(mongo) = %#v, %v", got, ok)
	}
	if _, ok := ServiceByID("missing"); ok {
		t.Fatal("ServiceByID(missing) unexpectedly found a service")
	}

	ids, err := ValidServiceIDs([]string{"redis", " mongo ", "redis"})
	if err != nil {
		t.Fatalf("ValidServiceIDs() error = %v", err)
	}
	if want := []string{"mongo", "redis"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ValidServiceIDs() = %#v; want %#v", ids, want)
	}
	if _, err := ValidServiceIDs([]string{"mongo", "unknown"}); err == nil {
		t.Fatal("ValidServiceIDs accepted an unknown ID")
	}
	if got, err := ValidServiceIDs(nil); err != nil || len(got) != 0 {
		t.Fatalf("ValidServiceIDs(nil) = %#v, %v; want empty", got, err)
	}
}

func TestServiceCatalogKeepsIndividualPersistenceTargets(t *testing.T) {
	checks := map[string][]string{
		"postgres":    {"pg-data:/var/lib/postgresql"},
		"redis":       {"redis-data:/data"},
		"dynamodb":    {"dynamodb-data:/home/dynamodblocal/data"},
		"nats":        {"nats-data:/data"},
		"jaeger":      {"jaeger-data:/badger"},
		"mailpit":     {"mailpit-data:/data"},
		"wiremock":    {"wiremock-data:/home/wiremock"},
		"code-server": {"code-server-config:/home/coder/.config", "code-server-data:/home/coder/.local/share/code-server"},
		"caddy":       {"caddy-data:/data", "caddy-config:/config"},
	}
	for id, want := range checks {
		service, ok := ServiceByID(id)
		if !ok {
			t.Fatalf("ServiceByID(%q) = false", id)
		}
		if !reflect.DeepEqual(service.Volumes, want) {
			t.Fatalf("%s volumes = %#v; want %#v", id, service.Volumes, want)
		}
	}
}

func TestServiceCatalogPersistenceMappingsAreExplicit(t *testing.T) {
	ephemeral := map[string]bool{
		"nginx":          true,
		"otel-collector": true,
		"traefik":        true,
	}
	for _, service := range Services {
		if len(service.Volumes) == 0 {
			if !ephemeral[service.ID] {
				t.Errorf("service %q has no explicitly reviewed persistence mapping", service.ID)
			}
			continue
		}
		seenDestinations := make(map[string]bool, len(service.Volumes))
		for _, raw := range service.Volumes {
			source, destination, ok := composeVolumeParts(raw)
			if !ok || !validComposeIdentifier(source) || !filepath.IsAbs(destination) {
				t.Errorf("service %q has invalid catalog persistence mapping %q", service.ID, raw)
			}
			if seenDestinations[destination] {
				t.Errorf("service %q repeats persistence destination %q", service.ID, destination)
			}
			seenDestinations[destination] = true
		}
	}
}

func TestServiceCatalogPersistenceRuntimeIsExplicit(t *testing.T) {
	checks := map[string]struct {
		command     []string
		workingDir  string
		environment map[string]string
	}{
		"dynamodb": {
			command:    []string{"-jar", "DynamoDBLocal.jar", "-sharedDb", "-dbPath", "./data"},
			workingDir: "/home/dynamodblocal",
		},
		"nats": {
			command: []string{"-js", "-sd", "/data", "-m", "8222"},
		},
		"jaeger": {
			environment: map[string]string{
				"SPAN_STORAGE_TYPE":      "badger",
				"BADGER_EPHEMERAL":       "false",
				"BADGER_DIRECTORY_VALUE": "/badger/data",
				"BADGER_DIRECTORY_KEY":   "/badger/key",
			},
		},
		"mailpit": {
			environment: map[string]string{"MP_DATABASE": "/data/mailpit.db"},
		},
		"code-server": {
			command:    []string{"--bind-addr", "0.0.0.0:8080", "/home/coder/project"},
			workingDir: "/home/coder/project",
		},
	}
	for id, want := range checks {
		service, ok := ServiceByID(id)
		if !ok {
			t.Fatalf("ServiceByID(%q) = false", id)
		}
		if !reflect.DeepEqual(service.Command, want.command) || service.WorkingDir != want.workingDir {
			t.Errorf("%s runtime = command %q working_dir %q; want %q %q", id, service.Command, service.WorkingDir, want.command, want.workingDir)
		}
		for key, value := range want.environment {
			if service.Environment[key] != value {
				t.Errorf("%s environment[%q] = %q; want %q", id, key, service.Environment[key], value)
			}
		}
	}
}

func TestServiceGroupingAndPortMapping(t *testing.T) {
	grouped := ServicesByCategory()
	if len(grouped[CategoryDatabaseSQL]) != 5 || len(grouped[CategoryCache]) != 3 || len(grouped[CategoryDevTools]) != 6 {
		t.Fatalf("unexpected category sizes: SQL=%d cache=%d dev=%d", len(grouped[CategoryDatabaseSQL]), len(grouped[CategoryCache]), len(grouped[CategoryDevTools]))
	}
	if got := (PortMapping{Internal: 5432}).DockerFlag(); got != "5432:5432" {
		t.Fatalf("default DockerFlag() = %q", got)
	}
	if got := (PortMapping{Internal: 53, Host: 1053, Protocol: "udp"}).DockerFlag(); got != "1053:53/udp" {
		t.Fatalf("custom DockerFlag() = %q", got)
	}
	if got := ServiceIDs(); len(got) != len(Services) || got[0] != "postgres" || got[len(got)-1] != "caddy" {
		t.Fatalf("ServiceIDs() does not preserve catalog order: %#v", got)
	}
	service, ok := ServiceByID("mongo")
	if !ok {
		t.Fatal("ServiceByID(mongo) unexpectedly failed")
	}
	service.Ports[0].Host = 9999
	service.Environment["MONGO_INITDB_ROOT_PASSWORD"] = "changed"
	canonical, _ := ServiceByID("mongo")
	if canonical.Ports[0].Host == 9999 || canonical.Environment["MONGO_INITDB_ROOT_PASSWORD"] != "dev" {
		t.Fatal("ServiceByID returned mutable catalog internals")
	}
}

func TestServiceWithPortOverrideReplacesPublishedHostPort(t *testing.T) {
	service, ok := ServiceByID("wiremock")
	if !ok {
		t.Fatal("wiremock is missing from the service catalog")
	}
	overridden, err := ServiceWithPortOverride(service, map[string][]PortMapping{
		"wiremock": {{Host: 18080, Internal: 8080}},
	})
	if err != nil {
		t.Fatalf("ServiceWithPortOverride() error = %v", err)
	}
	if got := overridden.Ports; !reflect.DeepEqual(got, []PortMapping{{Host: 18080, Internal: 8080}}) {
		t.Fatalf("overridden ports = %#v", got)
	}
	if got := service.Ports; !reflect.DeepEqual(got, []PortMapping{{Host: 8080, Internal: 8080, Protocol: "tcp"}}) {
		t.Fatalf("catalog service mutated = %#v", got)
	}
}

func TestServiceWithPortOverrideRejectsUndeclaredInternalPort(t *testing.T) {
	service, _ := ServiceByID("wiremock")
	if _, err := ServiceWithPortOverride(service, map[string][]PortMapping{
		"wiremock": {{Host: 18080, Internal: 9999}},
	}); err == nil || !strings.Contains(err.Error(), "undeclared internal port") {
		t.Fatalf("error = %v; want undeclared internal port", err)
	}
}

func TestCodeServerMountsProjectAndUsesBrowserRuntime(t *testing.T) {
	service, ok := ServiceByID("code-server")
	if !ok {
		t.Fatal("ServiceByID(code-server) unexpectedly failed")
	}
	if !service.MountProject || service.WorkingDir != "/home/coder/project" {
		t.Fatalf("code-server project settings = %#v; want project mount and working directory", service)
	}
	if !reflect.DeepEqual(service.Command, []string{"--bind-addr", "0.0.0.0:8080", "/home/coder/project"}) {
		t.Fatalf("code-server command = %#v", service.Command)
	}
}
