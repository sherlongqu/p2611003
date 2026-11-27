/* Report for public — interface behavior, no dependencies. */
(() => {
  "use strict";

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
  const docEl = document.documentElement;

  /* -------------------------------------------------------------- search */
  // Press F to jump to the search field, unless the user is already typing or
  // using a browser/OS shortcut.
  const searchInput = document.getElementById("searchInput");
  if (searchInput) {
    document.addEventListener("keydown", (event) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return;
      if (event.key !== "f" && event.key !== "F") return;
      const target = event.target;
      if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT" || target.isContentEditable)) return;
      event.preventDefault();
      searchInput.focus();
      searchInput.scrollIntoView({ block: "center", behavior: "smooth" });
    });
  }

  /* ----------------------------------------------------------- card art */
  // Cards use their own 3:2 renders, with a dark variant. Swap the source
  // whenever data-theme changes so the dark theme is not hit by a bright sheet.
  const cardArt = $$("[data-card-art]");
  const syncCardArt = () => {
    const dark = docEl.dataset.theme === "dark";
    cardArt.forEach((img) => {
      const next = dark ? img.dataset.dark : img.dataset.light;
      if (!next) return;
      const ready = () => img.classList.add("is-ready");
      if (img.getAttribute("src") === next) {
        if (img.complete) ready();
        return;
      }
      img.classList.remove("is-ready");
      img.addEventListener("load", ready, { once: true });
      img.src = next;
    });
  };
  syncCardArt();
  if ("MutationObserver" in window) {
    new MutationObserver(syncCardArt).observe(docEl, {
      attributes: true,
      attributeFilter: ["data-theme"]
    });
  }

  /* ---------------------------------------------------------------- theme */
  const themeMeta = $('meta[name="theme-color"]');
  const paintThemeMeta = () => {
    if (themeMeta) {
      themeMeta.setAttribute("content", docEl.dataset.theme === "dark" ? "#131410" : "#f7f4ec");
    }
  };
  paintThemeMeta();

  const themeToggle = $("#themeToggle");
  if (themeToggle) {
    themeToggle.addEventListener("click", () => {
      const next = docEl.dataset.theme === "dark" ? "light" : "dark";
      docEl.dataset.theme = next;
      try { localStorage.setItem("pubdocs-theme", next); } catch (_) { /* ignore */ }
      paintThemeMeta();
      document.dispatchEvent(new CustomEvent("pubdocs:themechange", { detail: { theme: next } }));
    });
  }

  /* ------------------------------------------------ form auto-submit */
  $$("[data-autosubmit]").forEach(function (el) {
    el.addEventListener("change", function () {
      if (!el.form) return;
      if (el.form.requestSubmit) el.form.requestSubmit();
      else el.form.submit();
    });
  });

  $$("form[data-autosubmit-form]").forEach(function (form) {
    var input = form.querySelector('input[type="file"]');
    if (!input) return;
    input.addEventListener("change", function () {
      if (!input.files || !input.files.length) return;
      if (form.requestSubmit) form.requestSubmit();
      else form.submit();
    });
  });

  /* ---------------------------------------------------------------- toast */
  let toastTimer = 0;
  const toast = (message, isError = false) => {
    let el = $("#toast");
    if (!el) {
      el = document.createElement("div");
      el.id = "toast";
      el.className = "toast";
      el.setAttribute("role", "status");
      document.body.appendChild(el);
    }
    el.textContent = message;
    el.classList.toggle("is-error", isError);
    requestAnimationFrame(() => el.classList.add("is-visible"));
    clearTimeout(toastTimer);
    toastTimer = window.setTimeout(() => el.classList.remove("is-visible"), 2800);
  };
  window.pubdocsToast = toast;

  /* ---------------------------------------------------------------- copy */
  const copyText = async (text) => {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return;
    }
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    const ok = document.execCommand("copy");
    area.remove();
    if (!ok) throw new Error("copy failed");
  };

  $$("[data-copy-text]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      try {
        await copyText(btn.dataset.copyText || window.location.href);
        if (btn.querySelector("svg")) {
          toast("Link copied");
          return;
        }
        const old = btn.textContent;
        btn.textContent = "Copied";
        window.setTimeout(() => { btn.textContent = old; }, 1400);
      } catch (_) {
        toast("Copy failed. Select the link manually.", true);
      }
    });
  });

  $$("[data-copy-target]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const target = document.getElementById(btn.dataset.copyTarget);
      if (!target) return;
      try {
        await copyText(target.textContent);
        if (btn.querySelector("svg")) {
          toast("Link copied");
          return;
        }
        const old = btn.textContent;
        btn.textContent = "Copied";
        window.setTimeout(() => { btn.textContent = old; }, 1400);
      } catch (_) {
        toast("Copy failed. Select the content manually.", true);
      }
    });
  });

  /* ---------------------------------------------------------------- delete */
  $$("[data-delete-doc]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!window.confirm("Delete this document? This cannot be undone.")) return;
      btn.disabled = true;
      try {
        const res = await fetch(`/api/documents/${encodeURIComponent(btn.dataset.deleteDoc)}`, {
          method: "DELETE",
          headers: { Accept: "application/json" },
        });
        if (res.status === 401) {
          window.location.href = "/studio?next=" + encodeURIComponent(window.location.pathname);
          return;
        }
        if (!res.ok) throw new Error("delete failed");
        toast("Document deleted.");
        window.setTimeout(() => { window.location.href = "/"; }, 500);
      } catch (_) {
        btn.disabled = false;
        toast("Delete failed. Try again.", true);
      }
    });
  });

  $$("form[data-confirm]").forEach((form) => {
    form.addEventListener("submit", (event) => {
      if (!window.confirm(form.dataset.confirm || "Are you sure?")) {
        event.preventDefault();
      }
    });
  });

  /* ------------------------------------------------------- reading progress */
  const progress = $("#readProgress");
  const article = $("#prose");
  if (progress && article) {
    const update = () => {
      const top = article.getBoundingClientRect().top + window.scrollY - 130;
      const height = Math.max(article.offsetHeight - window.innerHeight + 260, 1);
      const ratio = Math.min(Math.max((window.scrollY - top) / height, 0), 1);
      progress.style.width = (ratio * 100).toFixed(2) + "%";
    };
    update();
    window.addEventListener("scroll", update, { passive: true });
    window.addEventListener("resize", update);
  }

  /* ------------------------------------------------------ document toolbar */
  const docBar = $(".doc-bar");
  if (docBar) {
    const readerStage = $("#readerStage");
    const reveal = () => {
      docBar.classList.remove("is-hidden");
      document.body.classList.remove("bar-hidden");
    };
    const conceal = () => {
      docBar.classList.add("is-hidden");
      document.body.classList.add("bar-hidden");
    };

    if (readerStage) {
      let idle = 0;
      const arm = () => {
        window.clearTimeout(idle);
        idle = window.setTimeout(conceal, 3200);
      };
      const zone = document.createElement("div");
      zone.className = "doc-hotzone";
      zone.setAttribute("aria-hidden", "true");
      document.body.appendChild(zone);
      zone.addEventListener("pointerenter", () => { reveal(); arm(); });
      ["pointerdown", "keydown", "focusin", "pointermove"].forEach((ev) => {
        document.addEventListener(ev, () => { reveal(); arm(); }, { passive: true });
      });
      arm();
    } else {
      let lastY = window.scrollY;
      let lockUntil = 0;
      const settle = (hidden) => {
        if (hidden) conceal();
        else reveal();
        lockUntil = performance.now() + 260;
      };
      window.addEventListener("scroll", () => {
        const y = window.scrollY;
        const dy = y - lastY;
        lastY = y;
        if (Math.abs(dy) < 10) return;
        if (performance.now() < lockUntil) return;
        if (y < 120) settle(false);
        else settle(dy > 0);
      }, { passive: true });
    }
  }

  /* ---------------------------------------------------------------- reader */
  const readerShell = $("main.doc-page--reader");
  const fullscreenBtn = $("#readerFullscreen");
  if (readerShell && fullscreenBtn && readerShell.requestFullscreen) {
    const syncFullscreen = () => {
      const on = document.fullscreenElement === readerShell;
      fullscreenBtn.setAttribute("aria-pressed", on ? "true" : "false");
      fullscreenBtn.setAttribute("aria-label", on ? "Exit fullscreen" : "Fullscreen");
      fullscreenBtn.title = on ? "Exit fullscreen" : "Fullscreen";
    };
    fullscreenBtn.addEventListener("click", () => {
      if (document.fullscreenElement) {
        document.exitFullscreen().catch(() => {});
      } else {
        readerShell.requestFullscreen().catch(() => {});
      }
    });
    document.addEventListener("fullscreenchange", syncFullscreen);
    syncFullscreen();
  }
  /* ------------------------------------------------------ toc scroll hint */
  const tocWrap = $(".toc-wrap");
  if (tocWrap) {
    const syncTocHint = () => {
      const overflow = tocWrap.scrollHeight - tocWrap.clientHeight;
      const scrollable = overflow > 4;
      tocWrap.classList.toggle("is-scrollable", scrollable);
      tocWrap.classList.toggle("at-end", scrollable && tocWrap.scrollTop >= overflow - 4);
    };
    syncTocHint();
    tocWrap.addEventListener("scroll", syncTocHint, { passive: true });
    window.addEventListener("resize", syncTocHint);
  }

  /* ----------------------------------------------------------- toc spy */
  const tocLinks = $$(".toc a");
  if (tocLinks.length) {
    const headings = tocLinks
      .map((link) => {
        let id = link.getAttribute("href").slice(1);
        try { id = decodeURIComponent(id); } catch (_) { /* keep raw id */ }
        const el = document.getElementById(id);
        return el ? { link, el } : null;
      })
      .filter(Boolean);

    const spy = () => {
      let current = headings[0];
      for (const item of headings) {
        if (item.el.getBoundingClientRect().top <= 140) current = item;
      }
      tocLinks.forEach((l) => l.classList.toggle("active", current && l === current.link));
    };
    if (headings.length) {
      spy();
      window.addEventListener("scroll", spy, { passive: true });
    }
  }

  /* ---------------------------------------------------------------- upload */
  const form = $("#uploadForm");
  if (!form) return;

  const input = $("#fileInput");
  const dropzone = $("#dropzone");
  const fileList = $("#fileList");
  const submitBtn = $("#uploadBtn");
  const hint = $("#uploadHint");
  const maxMB = Number(form.dataset.maxMb || 50);
  const items = [];
  let uploading = false;

  const keyOf = (file) => `${file.name}:${file.size}:${file.lastModified}`;
  const humanSize = (n) => {
    if (n < 1024) return n + " B";
    if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
    return (n / 1048576).toFixed(1) + " MB";
  };

  const syncButton = () => {
    if (!submitBtn) return;
    submitBtn.disabled = uploading || items.length === 0;
    submitBtn.textContent = uploading ? "Uploading…" : items.length > 1 ? `Upload ${items.length} files` : "Start upload";
  };

  const addFiles = (fileListLike) => {
    const incoming = Array.from(fileListLike || []);
    let rejected = 0;
    incoming.forEach((file) => {
      if (file.size > maxMB * 1024 * 1024) { rejected++; return; }
      if (file.size === 0) { rejected++; return; }
      if (items.some((it) => keyOf(it.file) === keyOf(file))) return;
      items.push({ file, el: null, state: "pending" });
    });
    if (rejected) toast(`Ignored ${rejected} file(s) over ${maxMB} MB or empty.`, true);
    renderList();
  };

  const renderList = () => {
    if (!fileList) return;
    fileList.textContent = "";
    items.forEach((item) => {
      const wrap = document.createElement("div");
      wrap.className = "file-item";

      const row = document.createElement("div");
      row.className = "file-row";

      const name = document.createElement("span");
      name.className = "file-name";
      name.textContent = item.file.name;

      const size = document.createElement("span");
      size.className = "file-size";
      size.textContent = humanSize(item.file.size);

      const state = document.createElement("span");
      state.className = "file-state";
      state.textContent = item.state === "done" ? "Done" : item.state === "error" ? "Failed" : "";

      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "file-remove";
      remove.setAttribute("aria-label", "Remove " + item.file.name);
      remove.textContent = "×";
      remove.addEventListener("click", () => {
        if (uploading) return;
        const idx = items.indexOf(item);
        if (idx >= 0) items.splice(idx, 1);
        renderList();
      });

      const bar = document.createElement("div");
      bar.className = "file-bar";
      const fill = document.createElement("span");
      bar.appendChild(fill);

      row.append(name, size, state, remove);
      wrap.append(row, bar);
      fileList.appendChild(wrap);
      item.el = { state, fill };
    });
    if (hint) {
      hint.textContent = items.length > 1
        ? "With multiple files, each uses its own file name as the title; the description and tags apply to all of them."
        : "PDF, HTML, Markdown, plain text and images are supported.";
    }
    syncButton();
  };

  if (input) {
    input.addEventListener("change", () => {
      addFiles(input.files);
      input.value = "";
    });
  }

  if (dropzone) {
    ["dragenter", "dragover"].forEach((evt) => {
      dropzone.addEventListener(evt, (e) => {
        e.preventDefault();
        dropzone.classList.add("is-drag");
      });
    });
    ["dragleave", "drop"].forEach((evt) => {
      dropzone.addEventListener(evt, (e) => {
        e.preventDefault();
        dropzone.classList.remove("is-drag");
      });
    });
    dropzone.addEventListener("drop", (e) => {
      if (e.dataTransfer && e.dataTransfer.files) addFiles(e.dataTransfer.files);
    });
    dropzone.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        if (input) input.click();
      }
    });
  }

  const uploadOne = (item, title, description, tags) => new Promise((resolve, reject) => {
    const data = new FormData();
    data.append("file", item.file, item.file.name);
    if (title) data.append("title", title);
    if (description) data.append("description", description);
    if (tags) data.append("tags", tags);

    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/upload");
    xhr.responseType = "json";
    xhr.upload.addEventListener("progress", (e) => {
      if (!e.lengthComputable || !item.el) return;
      const pct = Math.min((e.loaded / e.total) * 100, 99);
      item.el.fill.style.width = pct.toFixed(0) + "%";
    });
    xhr.addEventListener("load", () => {
      const body = xhr.response || {};
      if (xhr.status >= 200 && xhr.status < 300 && body.document) {
        if (item.el) item.el.fill.style.width = "100%";
        item.state = "done";
        if (item.el) { item.el.state.textContent = "Done"; item.el.state.classList.add("is-done"); }
        resolve(body);
      } else if (xhr.status === 401) {
        reject(new Error("AUTH"));
      } else {
        reject(new Error((body && body.error) || "Upload failed"));
      }
    });
    xhr.addEventListener("error", () => reject(new Error("Network error")));
    xhr.addEventListener("abort", () => reject(new Error("Canceled")));
    xhr.send(data);
  });

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (uploading || items.length === 0) return;

    const titleInput = form.querySelector('[name="title"]');
    const descInput = form.querySelector('[name="description"]');
    const tagsInput = form.querySelector('[name="tags"]');
    const title = titleInput ? titleInput.value.trim() : "";
    const description = descInput ? descInput.value.trim() : "";
    const tags = tagsInput ? tagsInput.value.trim() : "";

    uploading = true;
    syncButton();
    let first = null;
    let failed = 0;

    for (let i = 0; i < items.length; i += 1) {
      const item = items[i];
      if (item.state === "done") { if (!first) first = item.result; continue; }
      if (submitBtn) submitBtn.textContent = `Uploading ${i + 1}/${items.length}…`;
      try {
        const result = await uploadOne(item, items.length === 1 ? title : "", description, tags);
        item.result = result;
        if (!first) first = result;
      } catch (err) {
        failed += 1;
        item.state = "error";
        if (item.el) { item.el.state.textContent = "Failed"; item.el.state.classList.add("is-error"); }
        if (err && err.message === "AUTH") {
          window.location.href = "/studio?next=" + encodeURIComponent("/studio/upload");
          return;
        }
      }
    }

    uploading = false;
    syncButton();

    if (first && failed === 0) {
      let message = items.length > 1 ? "All documents published." : "Document published.";
      let delay = 550;
      const inlined = first.inline;
      if (inlined && (inlined.inlined || inlined.failed || inlined.remaining)) {
        const bits = [];
        if (inlined.inlined) bits.push(`inlined ${inlined.inlined} resource${inlined.inlined === 1 ? "" : "s"}`);
        if (inlined.failed) bits.push(`${inlined.failed} fetch failed`);
        if (inlined.remaining) bits.push(`${inlined.remaining} external link${inlined.remaining === 1 ? "" : "s"} left`);
        message += " · " + bits.join(" · ");
        delay = 1900;
      }
      toast(message);
      const url = (first.document && first.document.url) || (first.url) || "/";
      try {
        const parsed = new URL(url, window.location.origin);
        window.setTimeout(() => { window.location.href = parsed.pathname; }, delay);
      } catch (_) {
        window.setTimeout(() => { window.location.href = "/"; }, delay);
      }
    } else if (failed > 0) {
      toast(`${failed} file(s) failed. Try again.`, true);
    }
  });
})();
