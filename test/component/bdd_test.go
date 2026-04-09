//go:build component

package component

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/cucumber/godog"
)

func TestBDDBackendFeatures(t *testing.T) {
	runBDD(t, "features/backend")
}

func TestBDDFrontendFeatures(t *testing.T) {
	runBDD(t, "features/frontend")
}

func runBDD(t *testing.T, path string) {
	suite := godog.TestSuite{
		Name:                "loop-bdd",
		ScenarioInitializer: initializeScenario,
		Options: &godog.Options{
			Format:      "pretty",
			Paths:       []string{path},
			Tags:        os.Getenv("GODOG_TAGS"),
			Concurrency: envInt("GODOG_CONCURRENCY", 1),
			TestingT:    t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("BDD test suite failed")
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func initializeScenario(ctx *godog.ScenarioContext) {
	tc := NewTestContext()

	ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
		resp, err := tc.HTTPClient.Get(tc.BaseURL + "/api/health")
		if err != nil {
			return ctx, fmt.Errorf("server not reachable at %s: %w", tc.BaseURL, err)
		}
		resp.Body.Close()
		return ctx, nil
	})

	ctx.After(func(ctx context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		tc.cleanup()
		return ctx, nil
	})

	registerBackendSteps(ctx, tc)
	registerFrontendSteps(ctx, tc)
}
