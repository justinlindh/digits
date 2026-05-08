package web

import (
	"log/slog"
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
	Server   []changelogRelease
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
		} else {
			slog.Error("changelog: list lines failed", "household_id", hh.ID, "err", err)
		}
	}

	var idx *updates.ReleaseIndex
	if h.Releases != nil {
		idx = h.Releases.ReleaseIndex()
	}

	var data changelogData
	if idx != nil {
		data.Server = buildChangelogSection(idx, updates.ComponentServer, nil, h)
		data.Software = buildChangelogSection(idx, updates.ComponentPi, lines, h)
		data.Firmware = buildChangelogSection(idx, updates.ComponentFirmware, lines, h)
	}

	renderWith(w, h.tmplChangelog, "changelog-content", data)
}

func buildChangelogSection(idx *updates.ReleaseIndex, component string, lines []string, h *Handler) []changelogRelease {
	releases := idx.SortedReleases(component)
	out := make([]changelogRelease, 0, len(releases))

	var totalDevices int
	versionCounts := make(map[string]int)
	for _, number := range lines {
		infos := h.hub.AllDeviceInfo(number)
		for _, info := range infos {
			var ver string
			switch component {
			case updates.ComponentPi:
				ver = info.PiVersion
			case updates.ComponentFirmware:
				ver = info.FirmwareVersion
			default:
				continue
			}
			if ver != "" {
				versionCounts[ver]++
				totalDevices++
			}
		}
	}

	for _, r := range releases {
		out = append(out, changelogRelease{
			Version:    r.Version,
			Notes:      r.Notes,
			Date:       r.Date,
			PhoneCount: versionCounts[r.Version],
			TotalCount: totalDevices,
		})
	}
	return out
}
