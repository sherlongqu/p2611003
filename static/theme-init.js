(function () {
  try {
    var q = new URLSearchParams(location.search).get("theme");
    var theme = q === "light" || q === "dark" ? q : localStorage.getItem("pubdocs-theme");
    if (theme !== "light" && theme !== "dark") {
      theme = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
    }
    document.documentElement.dataset.theme = theme;
    if (q === "light" || q === "dark") localStorage.setItem("pubdocs-theme", theme);
  } catch (e) { /* no-op */ }
})();
