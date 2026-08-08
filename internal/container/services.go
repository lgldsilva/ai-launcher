package container

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lgldsilva/ai-launcher/internal/config"
)

// ServiceCategory groups infrastructure services in the TUI and in the
// generated Compose model used by later phases.
type ServiceCategory string

const (
	// CategoryDatabaseSQL identifies SQL databases.
	CategoryDatabaseSQL ServiceCategory = "Databases (SQL)"
	// CategoryDatabaseNoSQL identifies document, graph, and wide-column databases.
	CategoryDatabaseNoSQL ServiceCategory = "Databases (NoSQL)"
	// CategoryDatabaseVector identifies vector databases.
	CategoryDatabaseVector ServiceCategory = "Databases (Vector)"
	// CategoryDatabaseTS identifies time-series databases.
	CategoryDatabaseTS ServiceCategory = "Databases (Time-series)"
	// CategorySearch identifies search engines.
	CategorySearch ServiceCategory = "Search"
	// CategoryQueue identifies message queues and streams.
	CategoryQueue ServiceCategory = "Message Queues"
	// CategoryCache identifies in-memory caches.
	CategoryCache ServiceCategory = "Caches"
	// CategoryStorage identifies object-storage services.
	CategoryStorage ServiceCategory = "Object Storage"
	// CategoryMonitoring identifies metrics, logs, and traces.
	CategoryMonitoring ServiceCategory = "Monitoring"
	// CategoryAuth identifies identity and authentication services.
	CategoryAuth ServiceCategory = "Identity / Auth"
	// CategoryWorkflow identifies workflow and orchestration engines.
	CategoryWorkflow ServiceCategory = "Workflow / Orchestration"
	// CategoryDevTools identifies local developer infrastructure.
	CategoryDevTools ServiceCategory = "Dev Tools"
)

// PortMapping is kept in config so Options can persist the same typed value;
// the alias preserves the public container.PortMapping API for services and
// runtime callers.
type PortMapping = config.PortMapping

// Service describes one optional infrastructure dependency of an agent
// environment. It is data only: service lifecycle belongs to Phase 6.
type Service struct {
	ID           string
	Name         string
	Category     ServiceCategory
	Image        string
	Command      []string
	WorkingDir   string
	MountProject bool
	Ports        []PortMapping
	Volumes      []string
	Environment  map[string]string
	DependsOn    []string
	Healthcheck  string
	Description  string
}

// Services is the complete infrastructure catalog, kept in category order
// so the CLI, TUI, generated Compose, and tests are deterministic.
var Services = []Service{
	// Databases (SQL)
	service("postgres", "PostgreSQL", CategoryDatabaseSQL, "postgres:18", []PortMapping{port(5432)}, []string{"pg-data:/var/lib/postgresql"}, map[string]string{
		"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "dev", "POSTGRES_DB": "dev",
	}, postgresHealthcheck, "PostgreSQL 18 SQL database"),
	service("mysql", "MySQL", CategoryDatabaseSQL, "mysql:8", []PortMapping{port(3306)}, []string{"mysql-data:/var/lib/mysql"}, map[string]string{
		"MYSQL_ROOT_PASSWORD": "dev", "MYSQL_DATABASE": "dev",
	}, "mysqladmin ping -h localhost -u root -pdev", "MySQL 8 SQL database"),
	service("mariadb", "MariaDB", CategoryDatabaseSQL, "mariadb:11", []PortMapping{port(3306)}, []string{"mariadb-data:/var/lib/mysql"}, map[string]string{
		"MARIADB_ROOT_PASSWORD": "dev", "MARIADB_DATABASE": "dev",
	}, "healthcheck.sh --connect", "MariaDB 11 SQL database"),
	service("oracle", "Oracle Free", CategoryDatabaseSQL, "gvenzl/oracle-free:23-slim", []PortMapping{port(1521)}, []string{"oracle-data:/opt/oracle/oradata"}, map[string]string{
		"ORACLE_PASSWORD": "dev", "APP_USER": "dev", "APP_USER_PASSWORD": "dev",
	}, "healthcheck.sh", "Oracle Free SQL database"),
	service("sqlserver", "SQL Server", CategoryDatabaseSQL, "mcr.microsoft.com/mssql/server:2022-CU16-ubuntu-22.04", []PortMapping{port(1433)}, []string{"mssql-data:/var/opt/mssql"}, map[string]string{
		"ACCEPT_EULA": "Y", "MSSQL_SA_PASSWORD": "DevPassword123!",
	}, "/opt/mssql-tools18/bin/sqlcmd -S localhost -C -U sa -P 'DevPassword123!' -Q 'SELECT 1'", "Microsoft SQL Server 2022 database"),

	// Databases (NoSQL)
	service("mongo", "MongoDB", CategoryDatabaseNoSQL, "mongo:8", []PortMapping{port(27017)}, []string{"mongo-data:/data/db"}, map[string]string{
		"MONGO_INITDB_ROOT_USERNAME": "dev", "MONGO_INITDB_ROOT_PASSWORD": "dev",
	}, "mongosh --quiet --eval 'db.adminCommand(\"ping\")'", "MongoDB document database"),
	service("cassandra", "Cassandra", CategoryDatabaseNoSQL, "cassandra:5", []PortMapping{port(9042)}, []string{"cassandra-data:/var/lib/cassandra"}, nil, "cqlsh -e 'describe keyspaces'", "Cassandra wide-column database"),
	service("neo4j", "Neo4j", CategoryDatabaseNoSQL, "neo4j:5", []PortMapping{port(7474), port(7687)}, []string{"neo4j-data:/data"}, map[string]string{
		"NEO4J_AUTH": "neo4j/dev",
	}, "cypher-shell -u neo4j -p dev 'RETURN 1'", "Neo4j graph database"),
	serviceWithRuntime(service("dynamodb", "DynamoDB Local", CategoryDatabaseNoSQL, "amazon/dynamodb-local:2.6.0", []PortMapping{port(8000)}, []string{"dynamodb-data:/home/dynamodblocal/data"}, nil, dynamodbHealthcheck, "Amazon DynamoDB local emulator"), []string{"-jar", "DynamoDBLocal.jar", "-sharedDb", "-dbPath", "./data"}, "/home/dynamodblocal"),

	// Databases (Vector)
	service("qdrant", "Qdrant", CategoryDatabaseVector, "qdrant/qdrant:v1.13", []PortMapping{port(6333), port(6334)}, []string{"qdrant-data:/qdrant/storage"}, nil, "wget -qO- http://localhost:6333/healthz", "Qdrant vector database"),
	service("weaviate", "Weaviate", CategoryDatabaseVector, "semitechnologies/weaviate:1.28", []PortMapping{port(8080)}, []string{"weaviate-data:/var/lib/weaviate"}, map[string]string{
		"QUERY_DEFAULTS_LIMIT": "25", "AUTHENTICATION_ANONYMOUS_ACCESS_ENABLED": "true", "PERSISTENCE_DATA_PATH": "/var/lib/weaviate",
	}, "wget -qO- http://localhost:8080/v1/.well-known/ready", "Weaviate vector search database"),
	service("milvus", "Milvus", CategoryDatabaseVector, "milvusdb/milvus:v2.4", []PortMapping{port(19530)}, []string{"milvus-data:/var/lib/milvus"}, nil, "wget -qO- http://localhost:9091/healthz", "Milvus vector database"),
	service("chroma", "Chroma", CategoryDatabaseVector, "chromadb/chroma:0.6.3", []PortMapping{port(8000)}, []string{"chroma-data:/chroma/chroma"}, nil, "wget -qO- http://localhost:8000/api/v1/heartbeat", "Chroma embedding database"),
	service("pgvector", "pgvector", CategoryDatabaseVector, "pgvector/pgvector:pg16", []PortMapping{port(5432)}, []string{"pgvector-data:/var/lib/postgresql/data"}, map[string]string{
		"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "dev", "POSTGRES_DB": "dev",
	}, postgresHealthcheck, "PostgreSQL with pgvector extension"),

	// Databases (Time-series)
	service("influxdb", "InfluxDB", CategoryDatabaseTS, "influxdb:2", []PortMapping{port(8086)}, []string{"influxdb-data:/var/lib/influxdb2"}, map[string]string{
		"DOCKER_INFLUXDB_INIT_MODE": "setup", "DOCKER_INFLUXDB_INIT_USERNAME": "admin", "DOCKER_INFLUXDB_INIT_PASSWORD": "devpassword", "DOCKER_INFLUXDB_INIT_ORG": "dev", "DOCKER_INFLUXDB_INIT_BUCKET": "dev",
	}, "influx ping", "InfluxDB time-series database"),
	service("timescaledb", "TimescaleDB", CategoryDatabaseTS, "timescale/timescaledb:pg16", []PortMapping{port(5432)}, []string{"tsdb-data:/var/lib/postgresql/data"}, map[string]string{
		"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "dev", "POSTGRES_DB": "dev",
	}, postgresHealthcheck, "TimescaleDB time-series database"),
	service("clickhouse", "ClickHouse", CategoryDatabaseTS, "clickhouse/clickhouse-server:25.3", []PortMapping{port(8123), port(9000)}, []string{"ch-data:/var/lib/clickhouse"}, nil, "wget -qO- http://localhost:8123/ping", "ClickHouse analytical time-series database"),

	// Search
	service("elasticsearch", "Elasticsearch", CategorySearch, "elasticsearch:8", []PortMapping{port(9200)}, []string{"es-data:/usr/share/elasticsearch/data"}, map[string]string{
		"discovery.type": "single-node", "xpack.security.enabled": "false",
	}, "wget -qO- http://localhost:9200/_cluster/health", "Elasticsearch search engine"),
	service("opensearch", "OpenSearch", CategorySearch, "opensearchproject/opensearch:2", []PortMapping{port(9200)}, []string{"os-data:/usr/share/opensearch/data"}, map[string]string{
		"discovery.type": "single-node", "DISABLE_SECURITY_PLUGIN": "true",
	}, "wget -qO- http://localhost:9200/_cluster/health", "OpenSearch search engine"),
	service("meilisearch", "Meilisearch", CategorySearch, "getmeili/meilisearch:v1.12", []PortMapping{port(7700)}, []string{"ms-data:/meili_data"}, map[string]string{
		"MEILI_NO_ANALYTICS": "true",
	}, "wget -qO- http://localhost:7700/health", "Meilisearch search API"),
	service("typesense", "Typesense", CategorySearch, "typesense/typesense:0.25", []PortMapping{port(8108)}, []string{"ts-data:/data"}, map[string]string{
		"TYPESENSE_DATA_DIR": "/data", "TYPESENSE_API_KEY": "dev",
	}, "wget -qO- http://localhost:8108/health", "Typesense search engine"),

	// Message Queues
	service("kafka", "Apache Kafka", CategoryQueue, "bitnami/kafka:3.7", []PortMapping{port(9092)}, []string{"kafka-data:/bitnami/kafka"}, map[string]string{
		"KAFKA_CFG_NODE_ID": "0", "KAFKA_CFG_PROCESS_ROLES": "controller,broker", "KAFKA_CFG_CONTROLLER_QUORUM_VOTERS": "0@kafka:9093", "KAFKA_CFG_LISTENERS": "PLAINTEXT://:9092,CONTROLLER://:9093", "KAFKA_CFG_ADVERTISED_LISTENERS": "PLAINTEXT://kafka:9092", "KAFKA_CFG_CONTROLLER_LISTENER_NAMES": "CONTROLLER", "KAFKA_CFG_LISTENER_SECURITY_PROTOCOL_MAP": "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT", "ALLOW_PLAINTEXT_LISTENER": "yes",
	}, "kafka-topics.sh --bootstrap-server localhost:9092 --list", "Apache Kafka event streaming"),
	service("redpanda", "Redpanda", CategoryQueue, "redpandadata/redpanda:v25", []PortMapping{port(19092), port(8080)}, []string{"redpanda-data:/var/lib/redpanda/data"}, nil, "curl -fsS http://localhost:9644/v1/status/ready", "Redpanda Kafka-compatible streaming"),
	service("rabbitmq", "RabbitMQ", CategoryQueue, "rabbitmq:3.13-management", []PortMapping{port(5672), port(15672)}, []string{"rabbit-data:/var/lib/rabbitmq"}, nil, "rabbitmq-diagnostics -q ping", "RabbitMQ message broker"),
	serviceWithRuntime(service("nats", "NATS", CategoryQueue, "nats:2", []PortMapping{port(4222), port(8222)}, []string{"nats-data:/data"}, nil, "wget -qO- http://localhost:8222/healthz", "NATS messaging system"), []string{"-js", "-sd", "/data", "-m", "8222"}, ""),
	service("activemq", "ActiveMQ", CategoryQueue, "apache/activemq-classic:5.18", []PortMapping{port(61616), port(8161)}, []string{"amq-data:/opt/apache-activemq/data"}, nil, "wget -qO- http://localhost:8161/", "ActiveMQ message broker"),
	service("pulsar", "Apache Pulsar", CategoryQueue, "apachepulsar/pulsar:3.3", []PortMapping{port(6650), port(8080)}, []string{"pulsar-data:/pulsar/data"}, nil, "wget -qO- http://localhost:8080/admin/v2/brokers/health", "Apache Pulsar messaging and streaming"),

	// Caches
	service("redis", "Redis", CategoryCache, "redis:7", []PortMapping{port(6379)}, []string{"redis-data:/data"}, nil, "redis-cli ping", "Redis in-memory cache and data store"),
	service("valkey", "Valkey", CategoryCache, "valkey/valkey:8", []PortMapping{port(6379)}, []string{"valkey-data:/data"}, nil, "valkey-cli ping", "Valkey-compatible cache and data store"),
	service("dragonfly", "Dragonfly", CategoryCache, "docker.dragonflydb.io/dragonflydb/dragonfly:v1.34.1", []PortMapping{port(6379)}, []string{"dragonfly-data:/data"}, nil, "redis-cli -h localhost ping", "Dragonfly Redis-compatible cache and data store"),

	// Object Storage
	service("minio", "MinIO", CategoryStorage, "minio/minio:RELEASE.2025-02-28T09-55-16Z", []PortMapping{port(9000), port(9001)}, []string{"minio-data:/data"}, map[string]string{
		"MINIO_ROOT_USER": "minio", "MINIO_ROOT_PASSWORD": "minio123",
	}, "mc ready local", "S3-compatible object storage"),
	service("localstack", "LocalStack", CategoryStorage, "localstack/localstack:4", []PortMapping{port(4566)}, []string{"ls-data:/var/lib/localstack"}, map[string]string{
		"SERVICES": "s3,sqs,lambda", "DEBUG": "0",
	}, "wget -qO- http://localhost:4566/_localstack/health", "Local AWS service emulator"),

	// Monitoring
	service("prometheus", "Prometheus", CategoryMonitoring, "prom/prometheus:v3.4.2", []PortMapping{port(9090)}, []string{"prom-data:/prometheus"}, nil, "wget -qO- http://localhost:9090/-/ready", "Prometheus metrics database"),
	service("grafana", "Grafana", CategoryMonitoring, "grafana/grafana:11.6.0", []PortMapping{port(3000)}, []string{"grafana-data:/var/lib/grafana"}, nil, "wget -qO- http://localhost:3000/api/health", "Grafana dashboards and observability"),
	serviceWithRuntime(service("jaeger", "Jaeger", CategoryMonitoring, "jaegertracing/all-in-one:1.69.0", []PortMapping{port(16686), port(4317)}, []string{"jaeger-data:/badger"}, map[string]string{
		"SPAN_STORAGE_TYPE": "badger", "BADGER_EPHEMERAL": "false", "BADGER_DIRECTORY_VALUE": "/badger/data", "BADGER_DIRECTORY_KEY": "/badger/key",
	}, "wget -qO- http://localhost:14269/", "Jaeger distributed tracing"), nil, ""),
	service("loki", "Loki", CategoryMonitoring, "grafana/loki:3.4.2", []PortMapping{port(3100)}, []string{"loki-data:/loki"}, nil, "wget -qO- http://localhost:3100/ready", "Loki log aggregation"),
	service("otel-collector", "OpenTelemetry Collector", CategoryMonitoring, "otel/opentelemetry-collector:0.127.0", []PortMapping{port(4317), port(4318)}, nil, nil, "wget -qO- http://localhost:13133/", "OpenTelemetry telemetry collector"),

	// Identity / Auth
	service("keycloak", "Keycloak", CategoryAuth, "quay.io/keycloak/keycloak:26", []PortMapping{port(8080)}, []string{"keycloak-data:/opt/keycloak/data"}, map[string]string{
		"KEYCLOAK_ADMIN": "admin", "KEYCLOAK_ADMIN_PASSWORD": "admin",
	}, "wget -qO- http://localhost:8080/health/ready", "Keycloak identity and access management"),
	service("authentik", "Authentik", CategoryAuth, "ghcr.io/goauthentik/server:2025.4.1", []PortMapping{port(9000), port(9443)}, []string{"authentik-data:/data"}, map[string]string{
		"AUTHENTIK_SECRET_KEY": "dev-secret-key-change-me", // #nosec G101 -- intentionally non-secret local development default.
	}, "wget -qO- http://localhost:9000/-/health/ready/", "Authentik identity provider"),

	// Workflow / Orchestration
	service("airflow", "Apache Airflow", CategoryWorkflow, "apache/airflow:3", []PortMapping{port(8080)}, []string{"airflow-data:/opt/airflow"}, map[string]string{
		"AIRFLOW__CORE__EXECUTOR": "LocalExecutor", "_AIRFLOW_WWW_USER_USERNAME": "admin", "_AIRFLOW_WWW_USER_PASSWORD": "admin",
	}, "curl -fsS http://localhost:8080/health", "Apache Airflow workflow orchestration"),
	service("temporal", "Temporal", CategoryWorkflow, "temporalio/auto-setup:1.27.2", []PortMapping{port(7233), port(8233)}, []string{"temporal-data:/etc/temporal"}, map[string]string{
		"DB": "sqlite",
	}, "wget -qO- http://localhost:8233/", "Temporal workflow engine"),
	service("n8n", "n8n", CategoryWorkflow, "n8nio/n8n:1.95.3", []PortMapping{port(5678)}, []string{"n8n-data:/home/node/.n8n"}, map[string]string{
		"N8N_BASIC_AUTH_ACTIVE": "false",
	}, "wget -qO- http://localhost:5678/healthz", "n8n workflow automation"),

	// Dev Tools
	service("mailpit", "Mailpit", CategoryDevTools, "axllent/mailpit:v1.20.5", []PortMapping{port(1025), port(8025)}, []string{"mailpit-data:/data"}, map[string]string{
		"MP_DATABASE": "/data/mailpit.db",
	}, "wget -qO- http://localhost:8025/api/v1/info", "Local SMTP testing server"),
	service("wiremock", "WireMock", CategoryDevTools, "wiremock/wiremock:3.13.2", []PortMapping{port(8080)}, []string{"wiremock-data:/home/wiremock"}, nil, "wget -qO- http://localhost:8080/__admin/health", "WireMock HTTP API mock server"),
	serviceWithProject(serviceWithRuntime(service("code-server", "VS Code Server", CategoryDevTools, "codercom/code-server:4.131.0-39", []PortMapping{port(8080)}, []string{
		"code-server-config:/home/coder/.config", "code-server-data:/home/coder/.local/share/code-server",
	}, nil, "wget -qO- http://localhost:8080/healthz", "VS Code in the browser"), []string{
		"--bind-addr", "0.0.0.0:8080", "/home/coder/project",
	}, "/home/coder/project")),
	service("nginx", "NGINX", CategoryDevTools, "nginx:1-alpine", []PortMapping{port(80)}, nil, nil, "wget -qO- http://localhost/", "NGINX reverse proxy"),
	service("traefik", "Traefik", CategoryDevTools, "traefik:v3", []PortMapping{port(80), port(8080)}, nil, nil, "wget -qO- http://localhost:8080/ping", "Traefik edge router"),
	service("caddy", "Caddy", CategoryDevTools, "caddy:2", []PortMapping{port(80), port(443)}, []string{"caddy-data:/data", "caddy-config:/config"}, nil, "wget -qO- http://localhost/", "Caddy web server and reverse proxy"),
}

func port(internal int) PortMapping {
	return PortMapping{Internal: internal, Host: internal, Protocol: "tcp"}
}

func service(id, name string, category ServiceCategory, image string, ports []PortMapping, volumes []string, environment map[string]string, healthcheck, description string) Service {
	if environment == nil {
		environment = map[string]string{}
	}
	return Service{
		ID: id, Name: name, Category: category, Image: image, Ports: ports,
		Volumes: volumes, Environment: environment,
		Healthcheck: healthcheck, Description: description,
	}
}

func serviceWithRuntime(service Service, command []string, workingDir string) Service {
	service.Command = append([]string(nil), command...)
	service.WorkingDir = workingDir
	return service
}

func serviceWithProject(service Service) Service {
	service.MountProject = true
	return service
}

// ServiceIDs returns catalog IDs in category/catalog order.
func ServiceIDs() []string {
	ids := make([]string, 0, len(Services))
	for _, service := range Services {
		ids = append(ids, service.ID)
	}
	return ids
}

// ServiceCategories returns the distinct categories in catalog order.
func ServiceCategories() []ServiceCategory {
	seen := make(map[ServiceCategory]struct{})
	categories := make([]ServiceCategory, 0, len(Services))
	for _, service := range Services {
		if _, ok := seen[service.Category]; ok {
			continue
		}
		seen[service.Category] = struct{}{}
		categories = append(categories, service.Category)
	}
	return categories
}

// ServicesByCategory groups services while retaining the catalog order inside
// each group. Callers should use ServiceCategories when rendering headings,
// because a map deliberately has no iteration order.
func ServicesByCategory() map[ServiceCategory][]Service {
	grouped := make(map[ServiceCategory][]Service)
	for _, service := range Services {
		grouped[service.Category] = append(grouped[service.Category], cloneService(service))
	}
	return grouped
}

// ServiceByID resolves one catalog service by its stable ID.
func ServiceByID(id string) (Service, bool) {
	id = strings.TrimSpace(id)
	for _, service := range Services {
		if service.ID == id {
			return cloneService(service), true
		}
	}
	return Service{}, false
}

// ServiceWithPortOverride returns a service copy with its host-published
// mappings replaced by the configured override. The internal ports are kept
// explicit in the override so a remap such as 18080:8080 remains auditable in
// YAML and in the generated Compose file. An empty mapping list intentionally
// removes host publication without affecting Compose-network connectivity.
func ServiceWithPortOverride(service Service, overrides map[string][]PortMapping) (Service, error) {
	mappings, ok := overrides[service.ID]
	if !ok {
		return service, nil
	}
	for index, mapping := range mappings {
		if err := ValidatePortMapping(mapping); err != nil {
			return Service{}, fmt.Errorf("service %q port override %d: %w", service.ID, index+1, err)
		}
	}
	for _, mapping := range mappings {
		validInternal := false
		for _, catalogMapping := range service.Ports {
			if mapping.Internal == catalogMapping.Internal {
				validInternal = true
				break
			}
		}
		if !validInternal {
			return Service{}, fmt.Errorf("service %q port override targets undeclared internal port %d", service.ID, mapping.Internal)
		}
	}
	service.Ports = append([]PortMapping(nil), mappings...)
	return service, nil
}

func cloneService(service Service) Service {
	service.Command = append([]string(nil), service.Command...)
	service.Ports = append([]PortMapping(nil), service.Ports...)
	service.Volumes = append([]string(nil), service.Volumes...)
	service.DependsOn = append([]string(nil), service.DependsOn...)
	if service.Environment != nil {
		environment := service.Environment
		service.Environment = make(map[string]string, len(environment))
		for key, value := range environment {
			service.Environment[key] = value
		}
	}
	return service
}

// ValidServiceIDs returns a canonical sorted, deduplicated copy of ids and
// rejects unknown IDs. Empty entries are ignored like ValidStackIDs.
func ValidServiceIDs(ids []string) ([]string, error) {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := ServiceByID(id); !ok {
			return nil, fmt.Errorf("unknown service %q", id)
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}
