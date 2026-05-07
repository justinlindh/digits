package web

import (
	"net/http"

	"github.com/justinlindh/digits/server/internal/updates"
)

type changelogRelease struct {
	Version    string
	Notes      string
	Date       string
	PhoneCount int
	TotalCount int
}

type changelogData struct {
	Software []changelogRelease
	Firmware []changelogRelease
}

func (h *Handler) handleChangelog(w http.ResponseWriter, r *http.Request) {
	hh := h.activeHousehold(r)
	var lines []string
	if hh != nil && h.lineStore != nil {
		ll, err := h.lineStore.ListByHousehold(r.Context(), hh.ID)
		if err == nil {
			for _, l := range ll {
				lines = append(lines, l.Number)
			}
		}
	}

	var idx *updates.ReleaseIndex
	if h.Releases != nil {
		idx = h.Releases.ReleaseIndex()
	}

	var data changelogData
	if idx != nil {
		data.Software = buildChangelogSection(idx, updates.ComponentPi, lines, h)
		data.Firmware = buildChangelogSection(idx, updates.ComponentFirmware, lines, h)
	}

	renderWith(w, h.tmplChangelog, "changelog-content", data)
}

func buildChangelogSection(idx *updates.ReleaseIndex, component string, lines []string, h *Handler) []changelogRelease {
	releases := idx.SortedReleases(component)
	total := len(lines)
	out := make([]changelogRelease, 0, len(releases))

	versionCounts := make(map[string]int)
	for _, number := range lines {
		info := h.hub.DeviceInfo(number)
		if info == nil {
			continue
		}
		var ver string
		if component == updates.ComponentPi {
			ver = info.PiVersion
		} else {
			ver = info.FirmwareVersion
		}
		if ver != "" {
			versionCounts[ver]++
		}
	}

	for _, r := range releases {
		out = append(out, changelogRelease{
			Version:    r.Version,
			Notes:      r.Notes,
			Date:       r.Date,
			PhoneCount: versionCounts[r.Version],
			TotalCount: total,
		})
	}
	return out
}
