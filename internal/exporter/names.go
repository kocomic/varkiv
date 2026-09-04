package exporter

import (
	"fmt"
	"strings"

	"varkiv/internal/catalog"
)

func editionName(w catalog.Game, e catalog.Edition) string {
	work := strings.TrimSpace(w.DisplayTitle)
	edition := strings.TrimSpace(e.DisplayTitle)
	if edition == "" || strings.EqualFold(work, edition) {
		return work
	}
	return fmt.Sprintf("%s [%s]", work, edition)
}
