export {};

declare global {
  interface Window {
    GloutonConfig: {
      STATIC_CDN_URL: string;
    };
    GloutonPanel: GloutonPanelApp | undefined;
  }
}
class GloutonPanelApp {
  async start() {
    await import("./app");
  }
}

window.GloutonPanel = new GloutonPanelApp();
window.GloutonPanel.start();
