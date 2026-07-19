package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionHTTPAddressUsesReservedPortOnAllInterfaces(t *testing.T) {
	if productionHTTPAddress != "0.0.0.0:4000" {
		t.Fatalf("production HTTP address = %q, want all interfaces on reserved port %d", productionHTTPAddress, productionHTTPPort)
	}
}

func TestValidateRuntimeConfigurationRejectsProductionResourcesInDevelopment(t *testing.T) {
	base := runtimeConfiguration{
		Mode:              runtimeModeDevelopment,
		Address:           "127.0.0.1:18080",
		AllowedOriginPort: 15173,
		TmuxSocket:        "dmv-test-a1",
	}

	tests := []struct {
		name   string
		change func(*runtimeConfiguration)
		want   string
	}{
		{
			name: "implicit production tmux socket",
			change: func(configuration *runtimeConfiguration) {
				configuration.TmuxSocket = ""
			},
			want: "production tmux socket",
		},
		{
			name: "explicit production tmux socket",
			change: func(configuration *runtimeConfiguration) {
				configuration.TmuxSocket = productionTmuxSocket
			},
			want: "production tmux socket",
		},
		{
			name: "backend production port",
			change: func(configuration *runtimeConfiguration) {
				configuration.Address = "0.0.0.0:4000"
			},
			want: "reserved production port 4000",
		},
		{
			name: "IPv6 backend production port",
			change: func(configuration *runtimeConfiguration) {
				configuration.Address = "[::1]:4000"
			},
			want: "reserved production port 4000",
		},
		{
			name: "frontend production port",
			change: func(configuration *runtimeConfiguration) {
				configuration.AllowedOriginPort = productionHTTPPort
			},
			want: "reserved production port 4000",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := base
			test.change(&configuration)
			err := validateRuntimeConfiguration(configuration, gitCheckout{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateRuntimeConfigurationAllowsIsolatedDevelopment(t *testing.T) {
	err := validateRuntimeConfiguration(runtimeConfiguration{
		Mode:              runtimeModeDevelopment,
		Address:           "127.0.0.1:18080",
		AllowedOriginPort: 15173,
		TmuxSocket:        "dmv-test-a1",
	}, gitCheckout{Present: true, Branch: "feature"})
	if err != nil {
		t.Fatalf("isolated development configuration was rejected: %v", err)
	}
}

func TestValidateRuntimeConfigurationRestrictsProductionToMain(t *testing.T) {
	configuration := runtimeConfiguration{Mode: runtimeModeProduction}
	for _, allowed := range []gitCheckout{
		{},
		{Present: true, Branch: productionGitBranch},
	} {
		if err := validateRuntimeConfiguration(configuration, allowed); err != nil {
			t.Fatalf("allowed production checkout %#v was rejected: %v", allowed, err)
		}
	}

	for _, rejected := range []gitCheckout{
		{Present: true, Branch: "feature/unsafe"},
		{Present: true, Branch: ""},
	} {
		err := validateRuntimeConfiguration(configuration, rejected)
		if err == nil || !strings.Contains(err.Error(), "may only run from git branch \"main\"") {
			t.Fatalf("production checkout %#v error = %v", rejected, err)
		}
	}

	configuration.AddCurrentDirectory = true
	if err := validateRuntimeConfiguration(configuration, gitCheckout{Present: true, Branch: "main"}); err == nil ||
		!strings.Contains(err.Error(), "only available in development mode") {
		t.Fatalf("production add-current-directory error = %v", err)
	}
}

func TestValidateRuntimeConfigurationRejectsInvalidModeAndAddress(t *testing.T) {
	if err := validateRuntimeConfiguration(runtimeConfiguration{Mode: "staging"}, gitCheckout{}); err == nil {
		t.Fatal("invalid runtime mode was accepted")
	}
	if err := validateRuntimeConfiguration(runtimeConfiguration{
		Mode:       runtimeModeDevelopment,
		Address:    "not-an-address",
		TmuxSocket: "dmv-test-a1",
	}, gitCheckout{}); err == nil || !strings.Contains(err.Error(), "parse HTTP listen address") {
		t.Fatalf("invalid development address error = %v", err)
	}
}

func TestValidateRuntimeStartupRejectsProductionFeatureCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}

	repository := t.TempDir()
	runRuntimeGit(t, repository, "init", "--quiet")
	runRuntimeGit(t, repository, "symbolic-ref", "HEAD", "refs/heads/feature")
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repository); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	err = validateRuntimeStartup(runtimeConfiguration{Mode: runtimeModeProduction})
	if err == nil || !strings.Contains(err.Error(), "may only run from git branch \"main\"") {
		t.Fatalf("production startup error = %v", err)
	}
}

func TestGitCheckoutAtFindsCurrentWorktreeBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}

	nonRepository := t.TempDir()
	checkout, err := gitCheckoutAt(nonRepository)
	if err != nil {
		t.Fatal(err)
	}
	if checkout.Present {
		t.Fatalf("ordinary directory reported as git checkout: %#v", checkout)
	}

	repository := t.TempDir()
	runRuntimeGit(t, repository, "init", "--quiet")
	runRuntimeGit(t, repository, "symbolic-ref", "HEAD", "refs/heads/main")
	nested := filepath.Join(repository, "nested", "directory")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	checkout, err = gitCheckoutAt(nested)
	if err != nil {
		t.Fatal(err)
	}
	if !checkout.Present || checkout.Branch != "main" {
		t.Fatalf("main checkout = %#v", checkout)
	}

	runRuntimeGit(t, repository, "symbolic-ref", "HEAD", "refs/heads/feature")
	checkout, err = gitCheckoutAt(nested)
	if err != nil {
		t.Fatal(err)
	}
	if !checkout.Present || checkout.Branch != "feature" {
		t.Fatalf("feature checkout = %#v", checkout)
	}
}

func runRuntimeGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
