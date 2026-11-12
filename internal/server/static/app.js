async function j(method, url, body) {
  const r = await fetch(url, { method, body });
  const t = await r.text();
  let d = {};
  try {
    d = JSON.parse(t);
  } catch {
    d = { error: t };
  }
  if (!r.ok) throw new Error(d.error || `${r.status}`);
  return d;
}

// Unfilled {{slot}} hrefs would be fetched as relative URLs by the
// browser — strip them so the raw template preview doesn't 404.
function injectSvg(host, svg) {
  host.innerHTML = svg;
  host.querySelectorAll("[href],[xlink\\:href]").forEach((el) => {
    for (const a of ["href", "xlink:href"]) {
      if (el.getAttribute(a)?.includes("{{")) el.removeAttribute(a);
    }
  });
}

// aspectOf returns the svg's w/h ratio from its viewBox (falling back to
// width/height attrs) — used to size preview boxes so tall templates
// aren't clipped by a hardcoded ratio.
function aspectOf(svg) {
  const vb = svg?.viewBox?.baseVal;
  if (vb?.width > 0 && vb?.height > 0) return vb.width / vb.height;
  const w = svg?.width?.baseVal?.value;
  const h = svg?.height?.baseVal?.value;
  return w > 0 && h > 0 ? w / h : null;
}

document.addEventListener("alpine:init", () => {
  Alpine.data("editor", () => ({
    sites: [],
    site: "",
    configText: "",
    fonts: [],
    fontSel: "",
    editOpen: false,
    mapOpen: false,
    modalOpen: false,
    imgList: [],
    filesList: [],
    statusMsg: "",
    statusCls: "",
    backdrop: "",
    bgColor: "#888888",
    showRegions: true,
    mkColor: "#e45842",
    mkFont: "",
    draft: null, // {kind, slot, el, value, orig}
    draftKind: "",
    editTitle: "",
    dv: {
      text: "",
      color: "#ffffff",
      src: "",
      fill: "#ffffff",
      stroke: "#000000",
    },
    regions: null,
    tplBox: null,
    slotMap: {},
    mapAssign: {},
    mapElements: [],
    allSlots: [
      "image1",
      "image2",
      "image3",
      "image4",
      "image5",
      "image6",
      "text1",
      "text2",
      "text3",
      "text4",
      "text5",
      "text6",
    ],
    timer: null,

    init() {
      this.loadSites();
      this.loadFonts();
      // click an svg element directly (not just its region box)
      this.$refs.tpl.addEventListener("click", (e) => {
        const n = e.target.closest("[id]");
        const el = n && this.regions?.find((x) => x.id === n.id);
        if (el) this.openEditor(el);
      });
    },

    status(msg, ok) {
      this.statusMsg = msg;
      this.statusCls = msg ? (ok ? "ok" : "err") : "";
    },

    async loadSites() {
      let d;
      try {
        d = await j("GET", "/api/sites");
      } catch (e) {
        this.status(
          `can't load sites: ${e.message} — is -ui running from the repo root?`,
          false,
        );
        return;
      }
      this.sites = d.sites;
      this.$nextTick(() => {
        if (d.sites[0]) {
          this.site = d.sites[0];
          this.loadSite(d.sites[0]);
        }
      });
    },

    async loadSite(name) {
      try {
        const d = await j("GET", `/api/site?name=${encodeURIComponent(name)}`);
        this.configText = d.config;
        injectSvg(this.$refs.tpl, d.template || "");
        // Size the stage + preview box from the viewBox immediately —
        // without this they collapse/clip until analyze lands.
        const r = aspectOf(this.$refs.tpl.querySelector("svg"));
        if (r) {
          this.$refs.tplStage.style.aspectRatio = `${r}`;
          this.$refs.tplPreview.style.aspectRatio = `${r}`;
        }
        this.filesList = [];
        if (this.draft) this.closeEditor();
        this.preview();
        try {
          const a = await j("POST", "/api/template-analyze", d.config);
          this.regions = a.elements;
          this.tplBox = a.viewBox;
          for (const e of a.elements) if (e.slot) this.slotMap[e.id] = e.slot;
          this.drawRegions();
          const unmapped = a.elements.filter((e) => !e.slot).length;
          if (unmapped)
            this.status(
              `${unmapped} unmapped template region(s) — use map template… or the output keeps the outlines`,
              true,
            );
        } catch {
          this.regions = null;
          this.drawRegions();
        }
      } catch (e) {
        this.status(e.message, false);
      }
    },

    drawRegions() {
      const ov = this.$refs.tplOverlay;
      ov.innerHTML = "";
      if (!this.regions || !this.tplBox || this.tplBox.length !== 4) {
        ov.hidden = true;
        return;
      }
      const [minX, minY, vbW, vbH] = this.tplBox.map(Number);
      this.$refs.tplStage.style.aspectRatio = `${vbW} / ${vbH}`;
      this.$refs.tplPreview.style.aspectRatio = `${vbW} / ${vbH}`;
      for (const e of this.regions) {
        const d = document.createElement("div");
        d.dataset.el = e.id;
        const slot = this.slotMap[e.id];
        d.className = `region${e.tag === "text" ? " txt" : ""}${slot ? " on" : ""}`;
        const x = ((e.x - minX) / vbW) * 100;
        const y = ((e.y - minY) / vbH) * 100;
        const w = Math.max((e.w / vbW) * 100, 1.2);
        const h = Math.max((e.h / vbH) * 100, 1.2);
        d.style.cssText = `left:${x}%;top:${y}%;width:${w}%;height:${h}%`;
        const b = document.createElement("b");
        b.textContent = slot || e.id;
        d.append(b);
        d.onclick = () => this.openEditor(e);
        ov.append(d);
      }
      ov.hidden = !this.showRegions;
    },

    setBackdrop(css) {
      document.querySelectorAll(".preview").forEach((p) => {
        p.style.background = css;
      });
    },
    backdropChanged() {
      this.setBackdrop(
        this.backdrop === "custom" ? this.bgColor : this.backdrop,
      );
    },

    previewDebounced() {
      clearTimeout(this.timer);
      this.timer = setTimeout(() => this.preview(), 350);
    },
    async preview() {
      try {
        let body = this.configText;
        if (this.draft) {
          const o = JSON.parse(body);
          this.applyDraftTo(o);
          body = JSON.stringify(o);
        }
        const d = await j("POST", "/api/preview", body);
        injectSvg(this.$refs.gen, d.svg);
        const r = aspectOf(this.$refs.gen.querySelector("svg"));
        if (r) this.$refs.gen.style.aspectRatio = `${r}`;
        this.status("");
      } catch (e) {
        this.status(e.message, false);
      }
    },

    patch(fn) {
      let o;
      try {
        o = JSON.parse(this.configText);
      } catch {
        this.status("invalid JSON", false);
        return;
      }
      fn(o);
      this.configText = JSON.stringify(o, null, 2);
      this.preview();
    },

    // -- contextual slot editor --------------------------------
    applyDraftTo(o) {
      const v = this.draft.value;
      if (this.draft.kind === "text") {
        o.text ||= {};
        o.text[this.draft.slot] = v.color
          ? { text: v.text, color: v.color }
          : v.text;
      } else {
        o.images ||= {};
        o.images[this.draft.slot] =
          v.fill || v.stroke
            ? {
                src: v.src,
                ...(v.fill && { fill: v.fill }),
                ...(v.stroke && { stroke: v.stroke }),
              }
            : v.src;
      }
    },

    imgSrc(cur) {
      return typeof cur === "string" ? cur : cur?.src || "";
    },
    srcIsSvg() {
      const src = this.dv.src || "";
      return (
        src.toLowerCase().endsWith(".svg") || src.startsWith("data:image/svg")
      );
    },

    openEditor(el) {
      const slot = this.slotMap[el.id] || el.slot;
      if (!slot) {
        this.status(`${el.id} has no slot — use map template…`, false);
        return;
      }
      let o;
      try {
        o = JSON.parse(this.configText);
      } catch {
        this.status("invalid JSON", false);
        return;
      }
      const kind = el.tag === "text" ? "text" : "image";
      const cur = kind === "text" ? o.text?.[slot] : o.images?.[slot];
      this.dv =
        kind === "text"
          ? {
              text: typeof cur === "string" ? cur : cur?.text || "",
              color:
                typeof cur === "object" && cur
                  ? cur.color || "#ffffff"
                  : "#ffffff",
            }
          : {
              src: this.imgSrc(cur),
              fill: cur?.fill || "#ffffff",
              stroke: cur?.stroke || "#000000",
            };
      this.draft = {
        kind,
        slot,
        el: el.id,
        value: this.dv,
        orig: { ...this.dv },
      };
      this.draftKind = kind;
      document.querySelectorAll(".region.sel").forEach((r) => {
        r.classList.remove("sel");
      });
      document
        .querySelector(`.region[data-el="${el.id}"]`)
        ?.classList.add("sel");
      this.editTitle = `${slot} → ${el.id}`;
      this.editOpen = true;
    },

    closeEditor() {
      this.draft = null;
      this.editOpen = false;
      document.querySelectorAll(".region.sel").forEach((r) => {
        r.classList.remove("sel");
      });
      this.preview();
    },

    isDirty() {
      return (
        this.draft &&
        JSON.stringify(this.dv) !== JSON.stringify(this.draft.orig)
      );
    },
    changed() {
      this.preview();
    },

    sw(pre, post) {
      const t = this.$refs.seText;
      t.setRangeText(
        pre + t.value.slice(t.selectionStart, t.selectionEnd) + post,
        t.selectionStart,
        t.selectionEnd,
        "select",
      );
      this.dv.text = t.value;
      this.changed();
    },
    swColor() {
      this.sw(`[${this.mkColor}]`, "[/]");
    },
    swFont() {
      const fam = this.mkFont;
      if (!fam) return;
      this.mkFont = "";
      this.sw(`[font:${fam}]`, "[/]");
    },
    fontFamilies() {
      return [
        ...new Set(this.fonts.map((f) => f.family).filter(Boolean)),
      ].sort();
    },
    fontLabel(f) {
      return `${f.name}${f.family ? ` — ${f.family}` : ""}`;
    },

    applyEdit() {
      if (!this.draft) return;
      this.patch((o) => this.applyDraftTo(o));
      this.draft.orig = { ...this.dv };
      this.status(`${this.draft.slot} applied — press save to persist`, true);
    },
    resetRecolor() {
      this.dv.fill = "#ffffff";
      this.dv.stroke = "#000000";
      this.changed();
    },

    // image picker modal — writes into the open draft
    async openPick() {
      try {
        const d = await j("GET", "/api/images");
        if (!d.images.length) {
          this.status("no files in assets/images", false);
          return;
        }
        this.imgList = d.images;
        this.modalOpen = true;
      } catch (e) {
        this.status(e.message, false);
      }
    },
    assetUrl(i) {
      return `/asset/${i.path.replace("assets/", "")}`;
    },
    pickImg(i) {
      if (this.draft?.kind === "image") {
        this.dv.src = i.path;
        this.changed();
      }
      this.closeModal();
    },
    closeModal() {
      this.modalOpen = false;
    },

    async loadFonts() {
      try {
        const prev = this.fontSel;
        const d = await j("GET", "/api/fonts");
        this.fonts = d.fonts;
        this.fontSel = prev;
      } catch {
        /* no assets/fonts dir */
      }
    },
    fontChanged() {
      const path = this.fontSel;
      if (!path) return;
      const it = this.fonts.find((f) => f.path === path);
      const family =
        it?.family ||
        path
          .split("/")
          .pop()
          .replace(/\.[^.]+$/, "");
      this.patch((o) => {
        o.render ||= {};
        o.render.fontFiles = [path];
        o.render.fontFamily = family;
      });
    },

    // -- template mapping --------------------------------------
    elsFor(slot) {
      const want = slot.startsWith("text") ? "text" : "fig";
      return this.mapElements.filter(
        (e) => (e.tag === "text") === (want === "text"),
      );
    },
    mapOptLabel(e) {
      return `${e.id}${e.w ? ` (${Math.round(e.w)}×${Math.round(e.h)})` : ""}`;
    },
    async openMap() {
      try {
        const d = await j("POST", "/api/template-analyze", this.configText);
        this.regions = d.elements;
        this.tplBox = d.viewBox;
        this.mapElements = d.elements;
        for (const e of d.elements) if (e.slot) this.slotMap[e.id] = e.slot;
        this.mapAssign = {};
        for (const [el, slot] of Object.entries(this.slotMap))
          this.mapAssign[slot] = el;
        this.drawRegions();
        this.mapOpen = true;
      } catch (e) {
        this.status(e.message, false);
      }
    },
    mapAssigned(slot) {
      // one element per slot: selecting el for this slot clears any
      // other slot that held it; slotMap rebuilds on apply.
      const el = this.mapAssign[slot];
      for (const s of this.allSlots) {
        if (s !== slot && this.mapAssign[s] === el) this.mapAssign[s] = "";
      }
      this.slotMap = {};
      for (const s of this.allSlots) {
        if (this.mapAssign[s]) this.slotMap[this.mapAssign[s]] = s;
      }
      this.drawRegions();
    },
    mapCancel() {
      this.mapOpen = false;
    },
    async mapApply() {
      const mapping = {};
      for (const s of this.allSlots) {
        if (this.mapAssign[s]) mapping[s] = this.mapAssign[s];
      }
      if (!Object.keys(mapping).length) {
        this.status("no slots assigned", false);
        return;
      }
      try {
        const d = await j(
          "POST",
          "/api/template-fill",
          JSON.stringify({
            config: JSON.parse(this.configText),
            mapping,
          }),
        );
        this.patch((o) => {
          o.template = d.template;
        });
        this.mapOpen = false;
        this.status(`wrote ${d.template}`, true);
      } catch (e) {
        this.status(e.message, false);
      }
    },

    async save() {
      try {
        const name = this.site;
        await j(
          "POST",
          `/api/save?name=${encodeURIComponent(name)}`,
          this.configText,
        );
        this.status(`saved configs/${name}.json`, true);
      } catch (e) {
        this.status(e.message, false);
      }
    },

    fileUrl(f) {
      return `/out/${f}`;
    },
    async renderAll() {
      try {
        // SSE when the server streams: each wrote lands live instead
        // of one response after the slowest converter finishes.
        const resp = await fetch("/api/render", {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Accept: "text/event-stream",
          },
          body: this.configText,
        });
        if (!resp.ok) {
          const d = await resp.json().catch(() => ({}));
          throw new Error(d.error || `${resp.status} ${resp.statusText}`);
        }
        if (!resp.headers.get("content-type")?.includes("event-stream")) {
          const d = await resp.json();
          this.status(`wrote: ${d.files.join(", ")}`, true);
          this.filesList = d.files;
          return;
        }
        const rd = resp.body.getReader();
        const dec = new TextDecoder();
        const wrote = [];
        let buf = "";
        for (;;) {
          const { done, value } = await rd.read();
          if (done) break;
          buf += dec.decode(value, { stream: true });
          const frames = buf.split("\n\n");
          buf = frames.pop();
          for (const frame of frames) {
            const line = frame.trim();
            if (!line.startsWith("data:")) continue;
            const ev = JSON.parse(line.slice(5));
            if (ev.error) throw new Error(ev.error);
            if (ev.file) {
              wrote.push(ev.file);
              this.status(`wrote: ${wrote.join(", ")}`, true);
            }
            if (ev.done) this.filesList = ev.files;
          }
        }
      } catch (e) {
        this.status(e.message, false);
      }
    },
  }));
});
