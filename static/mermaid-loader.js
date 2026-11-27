/* Load Mermaid only when a diagram approaches the viewport, and redraw on theme changes. */
(function () {
  "use strict";

  var script = document.currentScript || document.getElementById("mermaidLoader");
  var src = script && script.getAttribute("data-src");
  if (!src) return;

  var originals = Array.prototype.slice.call(document.querySelectorAll("pre.mermaid"));
  if (!originals.length) return;

  var slots = originals.map(function (pre) {
    var slot = document.createElement("div");
    slot.className = "mermaid-slot";
    pre.parentNode.insertBefore(slot, pre);
    slot.appendChild(pre);
    return { slot: slot, source: pre.textContent };
  });

  var loading = false;
  var callbacks = [];

  function ensure(cb) {
    if (window.mermaid) { cb(); return; }
    callbacks.push(cb);
    if (loading) return;
    loading = true;
    var s = document.createElement("script");
    s.src = src;
    s.async = true;
    s.onload = function () {
      var queue = callbacks;
      callbacks = [];
      queue.forEach(function (fn) { fn(); });
    };
    s.onerror = function () { loading = false; };
    document.head.appendChild(s);
  }

  function currentTheme() {
    return document.documentElement.dataset.theme === "dark" ? "dark" : "default";
  }

  function render() {
    slots.forEach(function (item) {
      item.slot.textContent = "";
      var pre = document.createElement("pre");
      pre.className = "mermaid";
      pre.textContent = item.source;
      item.slot.appendChild(pre);
    });
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: currentTheme(),
      fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif"
    });
    mermaid.run({ querySelector: ".mermaid" });
  }

  function start() {
    ensure(render);
  }

  if ("IntersectionObserver" in window) {
    var started = false;
    var io = new IntersectionObserver(function (entries) {
      if (started) return;
      if (entries.some(function (entry) { return entry.isIntersecting; })) {
        started = true;
        io.disconnect();
        start();
      }
    }, { rootMargin: "800px 0px" });
    slots.forEach(function (item) { io.observe(item.slot); });
  } else {
    start();
  }

  var scheduled = false;
  function scheduleRedraw() {
    if (scheduled) return;
    scheduled = true;
    window.setTimeout(function () {
      scheduled = false;
      if (window.mermaid) render();
    }, 30);
  }

  document.addEventListener("p2611003:themechange", scheduleRedraw);

  // Also observe data-theme directly: this is independent of the order in
  // which app.js and the loader execute, and catches theme changes from any
  // future control.
  if ("MutationObserver" in window) {
    new MutationObserver(scheduleRedraw).observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"]
    });
  }
})();
