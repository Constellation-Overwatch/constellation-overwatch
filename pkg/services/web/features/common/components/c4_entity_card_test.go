package components

import (
	"context"
	"strings"
	"testing"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/shared"
)

func TestC4EntityCardRendersSupportedDisplayStyle(t *testing.T) {
	t.Parallel()

	entity := shared.EntityState{
		EntityID:   "00000000-0000-0000-0000-000000000001",
		EntityType: "operator_station",
		Name:       "Test Station",
	}

	tests := []struct {
		name       string
		isDetailed bool
		wantStyle  string
	}{
		{name: "collapsed", wantStyle: `style="display:none;"`},
		{name: "detailed", isDetailed: true, wantStyle: `style="display:block;"`},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var rendered strings.Builder
			if err := C4EntityCard(entity, tt.isDetailed).Render(context.Background(), &rendered); err != nil {
				t.Fatalf("render entity card: %v", err)
			}
			html := rendered.String()
			if !strings.Contains(html, tt.wantStyle) {
				t.Fatalf("entity card missing %s: %s", tt.wantStyle, html)
			}
			if strings.Contains(html, "zTemplUnsupportedStyleAttributeValue") {
				t.Fatalf("entity card contains unsupported templ style marker: %s", html)
			}
		})
	}
}
