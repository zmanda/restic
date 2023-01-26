package smb_test

import (
	"context"
	"os"
	"testing"

	"github.com/restic/restic/internal/backend/smb"
	"github.com/restic/restic/internal/backend/test"
	"github.com/restic/restic/internal/options"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

func mkdir(t testing.TB, dir string) {
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		t.Fatal(err)
	}
}

func newTestSuite(t testing.TB) *test.Suite {
	return &test.Suite{
		// NewConfig returns a config for a new temporary backend that will be used in tests.
		NewConfig: func() (interface{}, error) {

			cfg := smb.NewConfig()
			cfg.Address = "127.0.0.1"
			cfg.ShareName = "SMBShare"
			cfg.User = os.Getenv("RESTIC_TEST_SMB_USERNAME")
			cfg.Password = options.NewSecretString(os.Getenv("RESTIC_TEST_SMB_PASSWORD"))
			cfg.Connections = smb.DefaultConnections
			cfg.IdleTimeout = smb.DefaultIdleTimeout
			cfg.Domain = smb.DefaultDomain
			t.Logf("create new backend at %v", cfg.Address+cfg.ShareName)

			return cfg, nil
		},

		// CreateFn is a function that creates a temporary repository for the tests.
		Create: func(config interface{}) (restic.Backend, error) {
			cfg := config.(smb.Config)
			return smb.Create(context.TODO(), cfg)
		},

		// OpenFn is a function that opens a previously created temporary repository.
		Open: func(config interface{}) (restic.Backend, error) {
			cfg := config.(smb.Config)
			return smb.Open(context.TODO(), cfg)
		},

		// CleanupFn removes data created during the tests.
		Cleanup: func(config interface{}) error {
			cfg := config.(smb.Config)
			if !rtest.TestCleanupTempDirs {
				t.Logf("leaving test backend dir at %v", cfg.Path)
			}

			rtest.RemoveAll(t, cfg.Path)
			return nil
		},
	}
}

func TestBackendSMB(t *testing.T) {
	defer func() {
		if t.Skipped() {
			rtest.SkipDisallowed(t, "restic/backend/smb.TestBackendSMB")
		}
	}()
	//TODO remove hardcoding
	os.Setenv("RESTIC_TEST_SMB_USERNAME", "smbuser")
	os.Setenv("RESTIC_TEST_SMB_PASSWORD", "mGoWwqvgdnwtmh07")

	vars := []string{
		"RESTIC_TEST_SMB_USERNAME",
		"RESTIC_TEST_SMB_PASSWORD",
	}

	for _, v := range vars {
		if os.Getenv(v) == "" {
			t.Skipf("environment variable %v not set", v)
			return
		}
	}

	t.Logf("run tests")

	newTestSuite(t).RunTests(t)
}

func BenchmarkBackendSMB(t *testing.B) {
	//TODO remove hardcoding
	os.Setenv("RESTIC_TEST_SMB_USERNAME", "smbuser")
	os.Setenv("RESTIC_TEST_SMB_PASSWORD", "mGoWwqvgdnwtmh07")

	vars := []string{
		"RESTIC_TEST_SMB_USERNAME",
		"RESTIC_TEST_SMB_PASSWORD",
	}

	for _, v := range vars {
		if os.Getenv(v) == "" {
			t.Skipf("environment variable %v not set", v)
			return
		}
	}

	newTestSuite(t).RunBenchmarks(t)
}
