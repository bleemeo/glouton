export {}

declare let __webpack_public_path__: string;

declare global {
  interface Window {
    GloutonConfig: {
      STATIC_CDN_URL: string,
    };
    __webpack_public_path__: string,
    GloutonPanel: GloutonPanelApp | undefined;
  }
}
class GloutonPanelApp {
  start() {
    /* eslint-disable */
    __webpack_public_path__ =
      process.env.NODE_ENV === "production"
        ? window.GloutonConfig.STATIC_CDN_URL
        : "http://localhost:3015/";
    import("./app");
    /* eslint-enable */
  }
}

window.GloutonPanel = new GloutonPanelApp();
