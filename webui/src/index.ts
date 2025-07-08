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
    console.debug("env=", process.env.NODE_ENV)
    const cdnUrl = process.env.NODE_ENV === "production" ? window.GloutonConfig.STATIC_CDN_URL : "http://localhost:3015/";

    console.log("CDN URL:", cdnUrl);

    await import("./app");
  }
}

window.GloutonPanel = new GloutonPanelApp();
window.GloutonPanel.start()
