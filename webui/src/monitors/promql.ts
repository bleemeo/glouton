// PromQL-shaped helpers specific to blackbox probe metrics.

export function escapeLabelValue(v: string): string {
  return v.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

// instanceSelector builds the {instance="..."} fragment used by every
// probe_* metric. The `instance` label is set by relabel rules in
// prometheus/registry/registry.go from the meta-label
// __meta_bleemeo_target_agent, which the blackbox manager populates
// from the target's `name` field (falling back to URL when name is
// empty — see blackbox/config.go).
export function instanceSelector(name: string): string {
  return `{instance="${escapeLabelValue(name)}"}`;
}
