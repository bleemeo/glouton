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
