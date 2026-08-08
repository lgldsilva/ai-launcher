package container

const (
	containerArtifactDirName = ".ai-launcher"
	launcherBinaryName       = "ai-launcher"
	composeHealthcheckShell  = "CMD-SHELL"
	dependencyTarget         = "$TARGET"

	goModuleCachePath      = "~/go/pkg/mod"
	cargoRegistryPath      = "~/.cargo/registry"
	cargoGitPath           = "~/.cargo/git"
	mavenRepositoryPath    = "~/.m2/repository"
	gradleCachePath        = "~/.gradle/caches"
	gradleWrapperPath      = "~/.gradle/wrapper/dists"
	nugetPackagesPath      = "~/.nuget/packages"
	bundlerCachePath       = "~/.bundle/cache"
	aptInstallPrefix       = "RUN apt-get update && apt-get install -y --no-install-recommends \\\n"
	aptCleanupSuffix       = " && rm -rf /var/lib/apt/lists/*"
	dockerfileLauncherCopy = "COPY ai-launcher /usr/local/bin/ai-launcher\n"
	dockerfileConfigCopy   = "COPY install-config.yaml "
	postgresHealthcheck    = "pg_isready -U postgres"
	dynamodbHealthcheck    = "bash -c 'exec 3<>/dev/tcp/127.0.0.1/8000'"
)
