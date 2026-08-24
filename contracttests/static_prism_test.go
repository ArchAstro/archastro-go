// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

package contracttests

import (
	"context"
	"testing"

	"github.com/ArchAstro/archastro-go/platform"
)

func TestPrismUsesDeterministicStaticResponses(t *testing.T) {
	// The launcher must never opt into json-schema-faker's nondeterministic
	// dynamic response generation.
	for _, arg := range prismArgs() {
		if arg == "--dynamic" {
			t.Fatal("Prism contract tests must use static responses")
		}
	}

	// Cross the real SDK-to-Prism HTTP boundary repeatedly with the nested
	// Activity Feed response that previously alternated between 200 and 500.
	client := restClient(t)
	for attempt := 1; attempt <= 10; attempt++ {
		result, err := client.V1.ActivityFeed.List(context.Background(), platform.ActivityFeedListParams{})
		if err != nil {
			t.Fatalf("Activity Feed request %d failed: %v", attempt, err)
		}

		// A decoded result proves Prism returned a schema-valid response on every
		// attempt rather than crashing inside nested array example generation.
		if result == nil {
			t.Fatalf("Activity Feed request %d returned no result", attempt)
		}
	}
}
