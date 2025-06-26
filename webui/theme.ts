import { createSystem, defaultConfig, defineConfig } from "@chakra-ui/react";

export const system = createSystem(defaultConfig, {
    globalCss: {
    "html,body,#root,#content": {
      minW: "full",
      w: "full",
      minH: "full",
      h: "full",
    },
  },
  theme: {
    tokens: {
    },
    semanticTokens: {
        colors: {
            text: {
                value: { _light: "#2b3f54", _dark: "#ffffff" },
            },
            background: {
                value: { _light: "#ffffff", _dark: "#1a202c" },
            },
            sidebarbackground: {
                value: { _light: "#2a3f54", _dark: "#2a3f54" },
            },
            bleemeo: {
                value: { _light: "#467FCF", _dark: "#467FCF" },
            },
        }
    }
  },
})
