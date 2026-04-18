#!/usr/bin/env python3
"""Verify every pad on each net in a .kicad_pcb is physically reachable from every
other pad on the same net via routed traces + vias.

KiCad's built-in DRC misses split-net cases where every pad has *some* routed
trace but the net is broken into disjoint subgraphs (e.g., a dangling stub that
should have continued to a pad but doesn't). See hardware/pcb/v2/ERRATA.md for
the 2026-04-18 +5V incident this was written to catch.

Usage:
    tools/check-pcb-nets.py path/to/board.kicad_pcb [net1 net2 ...]

With no net arguments, checks every net on the board. Exits non-zero if any
split net is found.
"""
import math
import re
import sys
from collections import defaultdict


def _walk_blocks(src, tag):
    """Yield each top-level (tag ...) s-expression block in src."""
    i = 0
    while True:
        i = src.find(f"({tag}", i)
        if i == -1:
            return
        if i > 0 and src[i - 1] not in "\n\t":
            i += 1
            continue
        depth = 0
        j = i
        while j < len(src):
            if src[j] == "(":
                depth += 1
            elif src[j] == ")":
                depth -= 1
                if depth == 0:
                    break
            j += 1
        yield src[i : j + 1]
        i = j + 1


def _key(x, y, layer):
    return (round(x, 3), round(y, 3), layer)


def load_board(path):
    with open(path) as f:
        pcb = f.read()

    # Per-net connectivity graph: nodes are (x, y, layer); via creates edges
    # between F.Cu and B.Cu at the same (x, y). Pads of through-hole type and
    # vias bridge all layers.
    segments = defaultdict(list)  # net -> list[(start_key, end_key)]
    vias = defaultdict(list)      # net -> list[(x, y)]
    pads = defaultdict(list)      # net -> list[(ref, pad_name, x, y, layers)]

    # Segments
    for block in _walk_blocks(pcb, "segment"):
        net_m = re.search(r'\(net "([^"]+)"\)', block)
        if not net_m:
            continue
        s = re.search(r"\(start\s+([0-9.-]+)\s+([0-9.-]+)\)", block)
        e = re.search(r"\(end\s+([0-9.-]+)\s+([0-9.-]+)\)", block)
        lay = re.search(r'\(layer "([^"]+)"\)', block)
        if not (s and e and lay):
            continue
        net = net_m.group(1)
        segments[net].append(
            (
                _key(float(s.group(1)), float(s.group(2)), lay.group(1)),
                _key(float(e.group(1)), float(e.group(2)), lay.group(1)),
            )
        )

    # Vias
    for block in _walk_blocks(pcb, "via"):
        net_m = re.search(r'\(net "([^"]+)"\)', block)
        at = re.search(r"\(at\s+([0-9.-]+)\s+([0-9.-]+)\)", block)
        if not (net_m and at):
            continue
        vias[net_m.group(1)].append((float(at.group(1)), float(at.group(2))))

    # Nets that have a copper zone (pour) — BFS over traces alone misses pour
    # connectivity, so we skip these and flag in the output.
    zone_nets = set()
    for zone in _walk_blocks(pcb, "zone"):
        # KiCad 10 uses (net "NAME") as the first element inside zone
        net_m = re.search(r'\(zone\s+\(net\s+"([^"]+)"\)', zone)
        if net_m:
            zone_nets.add(net_m.group(1))

    # Footprint pads (absolute positions after rotation + translation)
    for fp in _walk_blocks(pcb, "footprint"):
        fp_at = re.search(r"\(at\s+([0-9.-]+)\s+([0-9.-]+)(?:\s+([0-9.-]+))?\)", fp)
        ref_m = re.search(r'\(property "Reference" "([^"]+)"', fp)
        if not (fp_at and ref_m):
            continue
        fx, fy = float(fp_at.group(1)), float(fp_at.group(2))
        frot = math.radians(float(fp_at.group(3)) or 0) if fp_at.group(3) else 0.0
        cos_r, sin_r = math.cos(frot), math.sin(frot)
        ref = ref_m.group(1)

        # pads inside this footprint only
        for pad in _walk_blocks(fp, "pad"):
            net_m = re.search(r'\(net (?:\d+\s+)?"([^"]+)"\)', pad)
            if not net_m:
                continue
            name_m = re.search(r'\(pad "([^"]+)" (smd|thru_hole|np_thru_hole|connect)', pad)
            at = re.search(r"\(at\s+([0-9.-]+)\s+([0-9.-]+)", pad)
            if not at:
                continue
            px, py = float(at.group(1)), float(at.group(2))
            ax = fx + px * cos_r - py * sin_r
            ay = fy + px * sin_r + py * cos_r
            # Layer(s) the pad touches
            layers_m = re.search(r'\(layers\s+([^)]+)\)', pad)
            layers_raw = layers_m.group(1) if layers_m else '"F.Cu"'
            layers = [t.strip('"') for t in layers_raw.split()]
            # SMD pads live on the top Cu layer of the list; through-hole pads
            # straddle all Cu layers.
            pad_type = name_m.group(2) if name_m else "smd"
            if pad_type in ("thru_hole", "np_thru_hole"):
                cu_layers = ["F.Cu", "B.Cu"]
            else:
                cu_layers = [lay for lay in layers if lay.endswith(".Cu") or lay == "*.Cu"]
                if "*.Cu" in cu_layers:
                    cu_layers = ["F.Cu", "B.Cu"]
            pad_name = (name_m.group(1) if name_m else "?")
            pads[net_m.group(1)].append((ref, pad_name, ax, ay, cu_layers))

    return segments, vias, pads, zone_nets


def check_net(net, segments, vias, pads):
    """Return list[(ref, pad_name)] that are isolated (not in the main cluster)."""
    # Build adjacency
    adj = defaultdict(set)
    for a, b in segments[net]:
        adj[a].add(b)
        adj[b].add(a)

    # Vias connect F.Cu <-> B.Cu at (x, y)
    for vx, vy in vias[net]:
        fkey = _key(vx, vy, "F.Cu")
        bkey = _key(vx, vy, "B.Cu")
        adj[fkey].add(bkey)
        adj[bkey].add(fkey)

    # Pads are nodes; through-hole pads bridge F.Cu and B.Cu
    pad_nodes = {}  # (ref, pad_name) -> list of node keys (one per layer it touches)
    for ref, pad_name, x, y, cu_layers in pads[net]:
        keys = [_key(x, y, lay) for lay in cu_layers]
        pad_nodes[(ref, pad_name)] = keys
        # Bridge all layers of a through-hole pad
        for k1 in keys:
            for k2 in keys:
                if k1 != k2:
                    adj[k1].add(k2)
                    adj[k2].add(k1)

    if not pad_nodes:
        return []

    # BFS from first pad's first node
    start_pad, start_keys = next(iter(pad_nodes.items()))
    visited = set(start_keys)
    q = list(start_keys)
    # Also include all nodes coincident with the start keys (in case traces touch the pad from other layers)
    while q:
        n = q.pop()
        for nxt in adj.get(n, ()):
            if nxt not in visited:
                visited.add(nxt)
                q.append(nxt)

    # Any pad with zero keys in visited is isolated
    isolated = []
    for pid, keys in pad_nodes.items():
        if not any(k in visited for k in keys):
            isolated.append(pid)
    return isolated


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(2)
    pcb_path = sys.argv[1]
    target_nets = sys.argv[2:]

    segments, vias, pads, zone_nets = load_board(pcb_path)
    nets = target_nets or sorted(pads.keys())

    bad = []
    for net in nets:
        if net not in pads:
            print(f"  {net}: no pads on this net, skipping")
            continue
        if net in zone_nets:
            print(f"  {net}: has copper zone, connectivity via pour not checked (skipped)")
            continue
        isolated = check_net(net, segments, vias, pads)
        if isolated:
            pad_list = ", ".join(f"{ref}.{p}" for ref, p in isolated)
            print(f"  {net}: SPLIT -- {len(isolated)} pad(s) unreachable: {pad_list}")
            bad.append(net)
        else:
            n = len(pads[net])
            print(f"  {net}: OK ({n} pad{'s' if n != 1 else ''} all connected)")

    if bad:
        print(f"\n{len(bad)} split net(s): {', '.join(bad)}")
        sys.exit(1)
    print(f"\nAll {len(nets)} nets fully connected.")


if __name__ == "__main__":
    main()
