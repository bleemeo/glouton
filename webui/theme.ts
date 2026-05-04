import { createSystem, defaultConfig, defineConfig } from "@chakra-ui/react";

const config = defineConfig({
  globalCss: {
    "html, body, #root, #main": {
      minH: "100%",
      bg: "surface.canvas",
      color: "fg.default",
    },
    body: {
      fontFeatureSettings: "'cv11', 'ss01', 'ss03'",
    },
  },
  theme: {
    tokens: {
      colors: {
        // Glouton/Bleemeo accent gradient endpoints. The mid-tones are
        // computed at use-site (linear-gradient) so the theme stays a
        // single source of truth.
        accent: {
          start: { value: "#4F8DF5" }, // blue
          mid: { value: "#8B5CF6" }, // violet
          end: { value: "#F97316" }, // orange
        },
        status: {
          ok: { value: "#10B981" }, // emerald
          warn: { value: "#F59E0B" }, // amber
          crit: { value: "#EF4444" }, // red
          info: { value: "#3B82F6" }, // blue
          muted: { value: "#9CA3AF" },
        },
      },
      fonts: {
        heading: {
          value:
            'ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
        },
        body: {
          value:
            'ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
        },
        mono: {
          value:
            'ui-monospace, SFMono-Regular, "JetBrains Mono", Menlo, Monaco, Consolas, monospace',
        },
      },
    },
    semanticTokens: {
      colors: {
        // Surface stack — three depths so cards/headers feel layered.
        surface: {
          canvas: {
            value: { _light: "#F7F8FA", _dark: "#0B0F17" },
          },
          panel: {
            value: { _light: "#FFFFFF", _dark: "#111826" },
          },
          subtle: {
            value: { _light: "#F1F3F6", _dark: "#1A2233" },
          },
        },
        border: {
          subtle: {
            value: { _light: "#E5E8EE", _dark: "#1F2937" },
          },
          default: {
            value: { _light: "#D1D5DB", _dark: "#2A3243" },
          },
        },
        fg: {
          default: {
            value: { _light: "#111827", _dark: "#E5E7EB" },
          },
          muted: {
            value: { _light: "#6B7280", _dark: "#9CA3AF" },
          },
          subtle: {
            value: { _light: "#9CA3AF", _dark: "#6B7280" },
          },
          accent: {
            value: { _light: "#4F8DF5", _dark: "#7AA8FA" },
          },
        },
        // Per-status semantic tokens for KPI cards. Pair foreground with
        // a tinted background so cards remain readable in both modes.
        kpi: {
          ok: {
            bg: {
              value: {
                _light: "rgba(16,185,129,0.10)",
                _dark: "rgba(16,185,129,0.14)",
              },
            },
            fg: {
              value: { _light: "#047857", _dark: "#34D399" },
            },
            ring: {
              value: { _light: "#10B981", _dark: "#10B981" },
            },
          },
          warn: {
            bg: {
              value: {
                _light: "rgba(245,158,11,0.12)",
                _dark: "rgba(245,158,11,0.18)",
              },
            },
            fg: {
              value: { _light: "#B45309", _dark: "#FBBF24" },
            },
            ring: {
              value: { _light: "#F59E0B", _dark: "#F59E0B" },
            },
          },
          crit: {
            bg: {
              value: {
                _light: "rgba(239,68,68,0.12)",
                _dark: "rgba(239,68,68,0.18)",
              },
            },
            fg: {
              value: { _light: "#B91C1C", _dark: "#FCA5A5" },
            },
            ring: {
              value: { _light: "#EF4444", _dark: "#EF4444" },
            },
          },
          neutral: {
            bg: {
              value: { _light: "#FFFFFF", _dark: "#111826" },
            },
            fg: {
              value: { _light: "#111827", _dark: "#E5E7EB" },
            },
            ring: {
              value: { _light: "#D1D5DB", _dark: "#2A3243" },
            },
          },
        },
      },
      gradients: {
        // Used on active range button, accent borders, status badge.
        accent: {
          value:
            "linear-gradient(90deg, {colors.accent.start} 0%, {colors.accent.mid} 50%, {colors.accent.end} 100%)",
        },
      },
    },
  },
});

export const system = createSystem(defaultConfig, config);
