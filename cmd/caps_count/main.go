//go:build ignore

package main

import (
	"fmt"
	"sort"

	"github.com/Neaox/overcast/internal/capabilities"
)

func main() {
	all := capabilities.AllCapabilities
	counts := map[string]map[string]int{}
	for _, c := range all {
		if counts[c.Service] == nil {
			counts[c.Service] = map[string]int{}
		}
		counts[c.Service][c.Status.String()]++
	}
	svcs := make([]string, 0, len(counts))
	for s := range counts {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	for _, svc := range svcs {
		m := counts[svc]
		total := 0
		for _, v := range m {
			total += v
		}
		fmt.Printf("%-20s total=%-3d supported=%-3d partial=%-3d wip=%-3d unsupported=%d\n",
			svc, total,
			m["Supported"], m["Partial"], m["WIP"], m["Unsupported"])
	}
}
