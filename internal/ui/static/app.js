// The page of memcore ui: review, browse and search. It talks only to its own server, with the token of the run.
// Every document is hostile text until DOMPurify cleans it, and no document may style or cover the page.
"use strict";

(() => {
  const { h, render } = preact;
  const { useState, useEffect, useLayoutEffect, useMemo, useRef } = preactHooks;
  const html = htm.bind(h);

  // The token arrives in the fragment once, and the page keeps it for reloads of this window.
  const TOKEN_KEY = "memcore-token";
  let token = "";
  try {
    if (/^#[0-9a-f]{64}$/.test(location.hash)) {
      sessionStorage.setItem(TOKEN_KEY, location.hash.slice(1));
      history.replaceState(null, "", "#review");
    }
    token = sessionStorage.getItem(TOKEN_KEY) || "";
  } catch (e) {
    token = /^#[0-9a-f]{64}$/.test(location.hash) ? location.hash.slice(1) : "";
    if (token) history.replaceState(null, "", "#review");
  }

  const main = document.getElementById("main");
  const notices = document.getElementById("notices");
  const pushButton = document.getElementById("push");
  // queue is the queue that the window shows. head is the newest tip of main that the event stream sent.
  const state = { queue: null, head: "", selected: 0, tree: null };

  // ---------- Helpers ----------

  const esc = (s) =>
    String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

  // el builds an element with attributes and children. Text children become text nodes, never HTML.
  function el(tag, attrs, ...children) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v === false || v == null) continue;
      if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
      else if (k === "class") e.className = v;
      else e.setAttribute(k, v === true ? "" : v);
    }
    for (const c of children.flat()) {
      if (c == null || c === false) continue;
      e.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return e;
  }

  async function api(path, body) {
    const opts = { headers: { "X-Memcore-Token": token } };
    if (body !== undefined) {
      opts.method = "POST";
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    const resp = await fetch(path, opts);
    if (!resp.ok) {
      const text = (await resp.text()).trim();
      const err = new Error(text || resp.statusText);
      err.status = resp.status;
      throw err;
    }
    return resp.json();
  }

  function notice(kind, ...children) {
    const n = el("div", { class: "notice " + kind, role: kind === "bad" ? "alert" : "status" }, ...children);
    notices.append(n);
    return n;
  }

  function clearNotices(kind) {
    for (const n of [...notices.children]) if (!kind || n.classList.contains(kind)) n.remove();
  }

  const short = (sha) => (sha || "").slice(0, 7);
  const plural = (n, word, many) => `${n} ${n === 1 ? word : many || word + "s"}`;
  const isMarkdown = (path) => /\.md$/i.test(path);

  // ---------- Markdown ----------

  // The renderer escapes raw HTML, so the person reviews hostile text as text. A mermaid block becomes a
  // placeholder that renderDiagrams fills.
  marked.use({
    gfm: true,
    renderer: {
      html(token) {
        return esc(token.text);
      },
      code(token) {
        if ((token.lang || "").trim().toLowerCase() === "mermaid") {
          return `<div data-mermaid="">${esc(token.text)}</div>`;
        }
        return `<pre><code>${esc(token.text)}</code></pre>`;
      },
    },
  });

  const DOC_PURIFY = { FORBID_TAGS: ["style"], FORBID_ATTR: ["style", "class", "id"] };

  // splitFrontmatter returns the lines of the frontmatter, and the body after it.
  function splitFrontmatter(text) {
    const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(text);
    if (!m) return { front: null, body: text };
    return { front: m[1], body: text.slice(m[0].length) };
  }

  // frontmatterTable shows each top-level key of the frontmatter, with the text of its value.
  function frontmatterTable(front) {
    const rows = [];
    for (const line of front.split(/\r?\n/)) {
      const m = /^([A-Za-z0-9_-]+):\s?(.*)$/.exec(line);
      if (m) rows.push([m[1], m[2]]);
      else if (rows.length) rows[rows.length - 1][1] += "\n" + line.trim();
    }
    return el(
      "table",
      { class: "frontmatter" },
      rows.map(([k, v]) => el("tr", {}, el("th", {}, k), el("td", {}, v.replace(/^"(.*)"$/, "$1"))))
    );
  }

  // renderMarkdown renders a document at path into a new element: its frontmatter as a table, its body as
  // sanitized HTML, its diagrams, and its links to other documents.
  function renderMarkdown(text, path) {
    const box = el("div", { class: "doc" });
    const { front, body } = splitFrontmatter(text);
    if (front !== null) box.append(frontmatterTable(front));
    const html = DOMPurify.sanitize(marked.parse(body), DOC_PURIFY);
    const content = el("div");
    content.innerHTML = html;
    renderDiagrams(content);
    rewriteLinks(content, path);
    box.append(content);
    return box;
  }

  // renderDiagrams draws each mermaid block with pele, sanitizes its SVG, and puts it in a shadow root, so its
  // styles reach nothing outside it.
  function renderDiagrams(root) {
    for (const holder of root.querySelectorAll("[data-mermaid]")) {
      const source = holder.textContent;
      const host = el("div", { class: "diagram" });
      try {
        const result = pele.render(source, { links: false, images: false });
        const svg = DOMPurify.sanitize(result.svg, { USE_PROFILES: { svg: true, svgFilters: true } });
        host.attachShadow({ mode: "open" }).innerHTML = svg;
      } catch (e) {
        host.append(el("p", { class: "diagram-error" }, `pele cannot draw this diagram: ${e.message}`), el("pre", {}, source));
      }
      holder.replaceWith(host);
    }
  }

  // resolve returns the path of a relative link from the document at from, or "" when it leaves the repo.
  function resolve(from, link) {
    const parts = from.split("/").slice(0, -1);
    for (const p of link.split("/")) {
      if (p === "" || p === ".") continue;
      if (p === "..") {
        if (!parts.length) return "";
        parts.pop();
      } else parts.push(p);
    }
    return parts.join("/");
  }

  // rewriteLinks opens a relative link to a document in the window, and a web link in the browser.
  function rewriteLinks(root, path) {
    for (const a of root.querySelectorAll("a[href]")) {
      const href = a.getAttribute("href");
      if (/^https?:/i.test(href) || /^mailto:/i.test(href)) {
        a.setAttribute("target", "_blank");
        a.setAttribute("rel", "noopener noreferrer");
      } else if (!href.startsWith("#") && !/^[a-z][a-z0-9+.-]*:/i.test(href)) {
        let target = "";
        try {
          target = resolve(path, decodeURI(href.split("#")[0]));
        } catch (e) {
          target = ""; // a malformed link stays inert
        }
        if (target) a.setAttribute("href", "#browse/" + encodeURIComponent(target));
        else a.removeAttribute("href");
      }
    }
  }

  // ---------- Diffs ----------

  // hunkLines returns the lines of a diff from its first hunk, without the headers of git.
  function hunkLines(diff) {
    const lines = diff.split("\n");
    const start = lines.findIndex((l) => l.startsWith("@@"));
    return start < 0 ? [] : lines.slice(start);
  }

  const wordChanges = (words) => hunkLines(words).some((l) => (l.startsWith("+") || l.startsWith("-")) && !l.startsWith("@@"));

  // wordsView shows a porcelain word diff over the raw text: removed words struck out, added words marked.
  function wordsView(words) {
    const box = el("div", { class: "words" });
    let first = true;
    for (const line of hunkLines(words)) {
      if (line.startsWith("@@")) {
        if (!first) box.append(el("span", { class: "hunk" }, "…"));
        first = false;
      } else if (line.startsWith("~")) box.append("\n");
      else if (line.startsWith("+")) box.append(el("ins", {}, line.slice(1)));
      else if (line.startsWith("-")) box.append(el("del", {}, line.slice(1)));
      else if (line.startsWith(" ")) box.append(line.slice(1));
    }
    return box;
  }

  // patchView shows a unified diff in monospace, with removed and added lines in colour.
  function patchView(patch) {
    const box = el("pre", { class: "patch" });
    const lines = hunkLines(patch);
    if (!lines.length) return el("p", { class: "muted" }, "The file changes no line of text.");
    for (const line of lines) {
      const kind = line.startsWith("@@") ? "hunk" : line.startsWith("+") ? "add" : line.startsWith("-") ? "del" : "ctx";
      box.append(el("span", { class: kind }, line || " "));
    }
    return box;
  }

  // versionView renders the text after the commit, or the text before it for a removed file.
  function versionView(file) {
    const removed = file.after === null;
    const text = removed ? file.before : file.after;
    const box = el("div");
    if (removed) box.append(el("p", {}, el("span", { class: "tag removed" }, "removed")));
    if (text === null) return el("p", { class: "muted" }, "Git holds no text of this file.");
    box.append(isMarkdown(file.path) ? renderMarkdown(text, file.path) : el("pre", { class: "patch" }, text));
    return box;
  }

  // defaultView picks the first view of a file: New version for an added or removed file, Changes for a changed
  // markdown file with word changes, and Patch otherwise.
  function defaultView(file) {
    if (file.before === null || file.after === null) return "version";
    if (isMarkdown(file.path) && wordChanges(file.words)) return "words";
    return "patch";
  }

  function fileView(file) {
    const views = { words: "Changes", patch: "Patch", version: "New version" };
    const body = el("div");
    const seg = el("div", { class: "seg", role: "group", "aria-label": "View of the file" });
    const show = (v) => {
      for (const b of seg.children) b.classList.toggle("on", b.dataset.view === v);
      body.replaceChildren(v === "words" ? wordsView(file.words) : v === "patch" ? patchView(file.patch) : versionView(file));
    };
    for (const [v, label] of Object.entries(views)) {
      const b = el("button", { type: "button", onclick: () => show(v) }, label);
      b.dataset.view = v;
      seg.append(b);
    }
    const status = file.before === null ? el("span", { class: "tag new" }, "new") : file.after === null ? el("span", { class: "tag removed" }, "removed") : null;
    const box = el("div", { class: "file" }, el("div", { class: "file-head" }, el("span", { class: "path" }, file.path), status, seg), body);
    show(defaultView(file));
    return box;
  }

  // ---------- Components ----------

  // preactOwns says whether Preact rendered #main last. A view of el must unmount Preact first, and a view of
  // Preact must start from an empty #main, so the two renderers never share the children of #main.
  let preactOwns = false;

  function plain() {
    if (preactOwns) {
      render(null, main);
      preactOwns = false;
    }
  }

  function mount(vnode) {
    if (!preactOwns) {
      main.replaceChildren();
      preactOwns = true;
    }
    render(vnode, main);
  }

  // Raw mounts a DOM node that renderMarkdown or a diff view built. The node passed DOMPurify where it holds a
  // document, so no component needs dangerouslySetInnerHTML.
  function Raw({ node }) {
    const ref = useRef(null);
    useLayoutEffect(() => {
      ref.current.replaceChildren(node);
    }, [node]);
    return html`<div ref=${ref}></div>`;
  }

  // Seg is a row of buttons that picks one view.
  function Seg({ label, views, value, onChange }) {
    return html`<div class="seg" role="group" aria-label=${label}>
      ${views.map(
        ([v, text]) =>
          html`<button type="button" key=${v} class=${v === value ? "on" : undefined} data-view=${v} onClick=${() => onChange(v)}>${text}</button>`
      )}
    </div>`;
  }

  // ancestors returns the folders above a path, the outermost first.
  function ancestors(path) {
    const parts = path.split("/").slice(0, -1);
    return parts.map((_, i) => parts.slice(0, i + 1).join("/"));
  }

  // nest puts the files of docs into their folders. A folder holds its path, its folders by name, and its files.
  function nest(docs) {
    const root = { path: "", dirs: new Map(), files: [] };
    for (const d of docs) {
      let node = root;
      for (const dir of ancestors(d.path)) {
        const name = dir.split("/").pop();
        if (!node.dirs.has(name)) node.dirs.set(name, { path: dir, dirs: new Map(), files: [] });
        node = node.dirs.get(name);
      }
      node.files.push(d);
    }
    return root;
  }

  const indexFirst = (a, b) => (a.path.endsWith("README.md") ? -1 : b.path.endsWith("README.md") ? 1 : a.path.localeCompare(b.path));

  // Tree lists the files and folders of docs: README.md first, then the files, then the folders. A folder shows
  // its children only when opened holds its path. current is the path that the tree marks, and badge returns extra
  // children of a file. A file is a link into Browse, unless the caller passes onPick.
  function Tree({ docs, current, opened, onToggle, badge, onPick }) {
    const root = useMemo(() => nest(docs), [docs]);
    const ref = useRef(null);
    const revealed = useRef("");
    // The link of current appears once its folders open, so each render checks for it, and scrolls to it once.
    useEffect(() => {
      const on = ref.current && ref.current.querySelector("a.on");
      if (on && revealed.current !== current) {
        revealed.current = current;
        on.scrollIntoView({ block: "nearest" });
      }
    });
    const file = (d) => {
      const pick = onPick
        ? (e) => {
            e.preventDefault();
            onPick(d);
          }
        : undefined;
      const extra = badge ? badge(d) : d.unreviewed ? " •" : null;
      return html`<li key=${d.path}>
        <a href=${"#browse/" + encodeURIComponent(d.path)} class=${d.path === current ? "on" : undefined} onClick=${pick}>${d.path.split("/").pop()}${extra}</a>
      </li>`;
    };
    const list = (node) => html`<ul>
      ${[...node.files].sort(indexFirst).map(file)}
      ${[...node.dirs.entries()]
        .sort((a, b) => a[0].localeCompare(b[0]))
        .map(([name, child]) => {
          const open = opened.has(child.path);
          return html`<li key=${child.path + "/"}>
            <button type="button" class="folder" aria-expanded=${open ? "true" : "false"} onClick=${() => onToggle(child.path)}>${name}/</button>
            ${open ? list(child) : null}
          </li>`;
        })}
    </ul>`;
    return html`<nav class="tree" aria-label="Documents" ref=${ref}>${list(root)}</nav>`;
  }

  // Rendered shows a markdown document as renderMarkdown builds it.
  function Rendered({ text, path }) {
    const node = useMemo(() => renderMarkdown(text, path), [text, path]);
    return html`<${Raw} node=${node} />`;
  }

  // Source shows the lines of a document, with line marked, and scrolls to it.
  function Source({ text, line }) {
    const ref = useRef(null);
    useEffect(() => {
      const hit = ref.current.querySelector(".line.hit");
      if (hit) hit.scrollIntoView({ block: "center" });
    }, [text, line]);
    return html`<pre class="source" ref=${ref}>${text
      .split("\n")
      .map((l, i) => html`<span class=${i + 1 === line ? "line hit" : "line"} data-line=${i + 1}>${l || " "}</span>`)}</pre>`;
  }

  // ---------- Finder ----------

  const findDialog = document.getElementById("find-dialog");
  const isMac = /Mac|iPhone|iPad/.test(navigator.platform);

  // Marked shows a path with its matched characters in <mark>, one mark for each run of characters.
  function Marked({ path, marks }) {
    const chars = Array.from(path);
    const on = new Set(marks);
    const parts = [];
    for (let i = 0; i < chars.length; ) {
      let j = i;
      while (j < chars.length && on.has(j) === on.has(i)) j++;
      const text = chars.slice(i, j).join("");
      parts.push(on.has(i) ? html`<mark>${text}</mark>` : text);
      i = j;
    }
    return parts;
  }

  // Finder asks the server for the files whose paths match the query, and opens the chosen one in Browse.
  function Finder() {
    const [found, setFound] = useState([]);
    const [selected, setSelected] = useState(0);
    const [error, setError] = useState("");
    const list = useRef(null);
    const latest = useRef(""); // the query in the input
    const shown = useRef(""); // the query of the files that the list shows
    useEffect(() => {
      const row = list.current && list.current.children[selected];
      if (row) row.scrollIntoView({ block: "nearest" });
    }, [selected, found]);
    const ask = async (q) => {
      latest.current = q;
      if (!q.trim()) {
        shown.current = q;
        setFound([]);
        setError("");
        return;
      }
      try {
        const r = await api("/api/find?q=" + encodeURIComponent(q));
        if (latest.current !== q) return; // a newer query runs
        shown.current = q;
        setFound(r.found);
        setSelected(0);
        setError("");
      } catch (e) {
        if (latest.current === q) setError(e.message);
      }
    };
    const go = (f) => {
      findDialog.close();
      const hash = "#browse/" + encodeURIComponent(f.path);
      if (location.hash !== hash) location.hash = hash;
      else {
        browseVisit++; // the same hash fires no hashchange, so Browse mounts again to reveal the document
        route();
      }
    };
    const onKey = (e) => {
      if (e.key === "ArrowDown") setSelected((s) => Math.min(s + 1, found.length - 1));
      else if (e.key === "ArrowUp") setSelected((s) => Math.max(s - 1, 0));
      else if (e.key === "Enter") {
        if (found[selected] && shown.current === latest.current) go(found[selected]); // never a file of an older query
      }
      else return;
      e.preventDefault();
    };
    let body = null;
    if (error) body = html`<p class="notice bad">${error}</p>`;
    else if (latest.current.trim() && !found.length) body = html`<p class="muted">No file matches.</p>`;
    // autofocus: showModal focuses the input at once, with no frame of the browser in between.
    return html`<input autofocus type="search" placeholder="Search files by name" aria-label="Search files by name" autocomplete="off"
        onInput=${(e) => ask(e.currentTarget.value)} onKeyDown=${onKey} />
      ${body}
      <ul class="found" ref=${list} role="listbox" aria-label="Files">
        ${found.map(
          (f, i) => html`<li key=${f.path} role="option" aria-selected=${i === selected ? "true" : "false"} class=${i === selected ? "on" : undefined}
            onClick=${() => go(f)} onMouseMove=${() => i !== selected && setSelected(i)}>
            <${Marked} path=${f.path} marks=${f.marks} />${f.unreviewed ? " •" : null}
          </li>`
        )}
      </ul>`;
  }

  // openFinder renders a new finder into its dialog, so each opening starts empty.
  function openFinder() {
    if (findDialog.open) {
      findDialog.querySelector("input").focus();
      return;
    }
    render(html`<${Finder} key=${Date.now()} />`, findDialog);
    findDialog.showModal();
  }

  // The close event comes as a task of its own, after a fast Escape and Cmd+P may have opened a new finder.
  findDialog.addEventListener("close", () => {
    if (!findDialog.open) render(null, findDialog);
  });
  findDialog.addEventListener("click", (e) => {
    if (e.target === findDialog) findDialog.close(); // a click on the backdrop
  });
  document.getElementById("find-key").textContent = isMac ? "⌘P" : "Ctrl+P";
  document.getElementById("find").addEventListener("click", openFinder);

  // Cmd+P on macOS, or Ctrl+P elsewhere, opens the finder from every view, also from an input. The browser
  // never prints the page. It runs before the keys of the views, which ignore a key with a modifier.
  document.addEventListener("keydown", (e) => {
    if (e.key.toLowerCase() !== "p" || e.altKey || e.shiftKey || (isMac ? !e.metaKey || e.ctrlKey : !e.ctrlKey || e.metaKey)) return;
    e.preventDefault();
    const other = document.querySelector("dialog[open]");
    if (other && other !== findDialog) return;
    openFinder();
  });

  // ---------- Review ----------

  const isCode = (entry) => entry.files.some((f) => f.kind === "skills" || f.kind === "bin");
  const seenKey = (entry) => "memcore-seen:" + (entry.commit || entry.subject);

  function isSeen(entry) {
    try {
      return localStorage.getItem(seenKey(entry)) === "1";
    } catch (e) {
      return false;
    }
  }

  function setSeen(entry, on) {
    try {
      if (on) localStorage.setItem(seenKey(entry), "1");
      else localStorage.removeItem(seenKey(entry));
    } catch (e) {
      /* the marker is a convenience only */
    }
  }

  function entryView(entry, index) {
    const box = el("section", { class: "entry", "data-index": index, tabindex: "-1" });
    if (isSeen(entry)) box.classList.add("seen");
    const seen = el("input", { type: "checkbox", "aria-label": "Seen", checked: isSeen(entry) });
    seen.addEventListener("change", () => {
      setSeen(entry, seen.checked);
      box.classList.toggle("seen", seen.checked);
    });
    const meta = [];
    if (entry.body) meta.push(entry.body);
    if (entry.fixups.length) meta.push(`${plural(entry.fixups.length, "fixup")} fold${entry.fixups.length === 1 ? "s" : ""} in.`);
    box.append(
      el(
        "div",
        { class: "entry-head" },
        el("span", { class: "sha" }, entry.commit ? short(entry.commit) : "new"),
        isCode(entry) ? el("span", { class: "tag code" }, "Code") : null,
        el("span", { class: "subject" }, entry.subject),
        el("span", { class: "grow" }),
        el("label", { class: "muted" }, seen, " Seen"),
        entry.commit ? el("button", { type: "button", onclick: () => askDrop(entry) }, "Drop") : null
      ),
      ...(meta.length ? [el("div", { class: "entry-meta" }, meta.join("\n"))] : []),
      ...entry.files.map(fileView)
    );
    box.addEventListener("click", () => select(index, false));
    return box;
  }

  // ordered returns the entries with the code first, each group newest first.
  function ordered(entries) {
    return [...entries.filter(isCode), ...entries.filter((e) => !isCode(e))];
  }

  async function loadQueue() {
    clearNotices("queue");
    plain();
    main.replaceChildren(el("p", { class: "muted" }, "Reading the queue."));
    let q;
    try {
      q = await api("/api/queue");
    } catch (e) {
      plain();
      main.replaceChildren(el("p", { class: "notice bad" }, `The queue cannot load: ${e.message}`));
      return;
    }
    state.queue = q;
    state.selected = 0;
    renderReview();
    clearNotices("queue");
    if (state.head && state.head !== q.main) showChanged(); // a commit landed while the queue loaded
  }

  function renderReview() {
    const q = state.queue;
    const entries = ordered(q.entries || []);
    const parts = [];
    if (q.warning) parts.push(el("div", { class: "notice warn" }, q.warning));
    if (!entries.length) {
      parts.push(el("p", { class: "muted" }, "Nothing waits for review."));
    } else {
      let header = q.folded
        ? `Push leaves ${plural(entries.length, "commit")}, after the fold.`
        : `The review queue holds ${plural(entries.length, "commit")}, each as it is.`;
      if (q.dropped) header += ` The fold drops ${plural(q.dropped, "commit")} that leave every document as it was.`;
      parts.push(el("p", { class: "muted" }, header));
      const code = entries.filter(isCode);
      entries.forEach((entry, i) => {
        if (i === 0 && code.length) parts.push(el("h2", { class: "group" }, "Code: skills and scripts"));
        if (i === code.length) parts.push(el("h2", { class: "group" }, "Documents"));
        parts.push(entryView(entry, i));
      });
    }
    state.entries = entries;
    plain();
    main.replaceChildren(...parts);
    pushButton.hidden = !entries.length;
    pushButton.textContent = `Push ${plural(entries.length, "commit")}`;
    select(state.selected, false);
  }

  function select(i, scroll) {
    const boxes = main.querySelectorAll(".entry");
    if (!boxes.length) return;
    state.selected = Math.max(0, Math.min(i, boxes.length - 1));
    boxes.forEach((b, j) => b.classList.toggle("selected", j === state.selected));
    if (scroll) boxes[state.selected].scrollIntoView({ block: "nearest", behavior: "smooth" });
  }

  function askDrop(entry) {
    const dialog = document.getElementById("drop-dialog");
    document.getElementById("drop-text").textContent =
      `Drop ${short(entry.commit)} "${entry.subject}"` + (entry.fixups.length ? `, with its ${plural(entry.fixups.length, "fixup")}?` : "?");
    const reason = document.getElementById("drop-reason");
    reason.value = "";
    dialog.returnValue = ""; // Escape keeps the last value, so a past answer must never confirm this one
    dialog.onclose = async () => {
      if (dialog.returnValue !== "drop") return;
      await act("/api/drop", { commit: entry.commit, reason: reason.value.trim() });
    };
    dialog.showModal();
    reason.focus();
  }

  function askPush() {
    const q = state.queue;
    if (!q || !state.entries.length) return;
    const dialog = document.getElementById("push-dialog");
    let text = `Push the ${plural(state.entries.length, "commit")} that this window shows to origin?`;
    if (state.entries.some(isCode)) text += " The queue changes skills or scripts, which run once they are pushed.";
    document.getElementById("push-text").textContent = text;
    dialog.returnValue = ""; // Escape keeps the last value, so a past answer must never confirm this one
    dialog.onclose = async () => {
      if (dialog.returnValue !== "push") return;
      await act("/api/push", { head: q.main });
    };
    dialog.showModal();
    document.getElementById("push-confirm").focus();
  }

  // act runs an action, shows its output, and loads the queue again.
  async function act(path, body) {
    clearNotices("result");
    try {
      const r = await api(path, body);
      const n = notice(r.error ? "bad result" : "result", el("pre", { class: "grow" }, r.error ? `${r.output}\n${r.error}`.trim() : r.output));
      n.append(el("button", { type: "button", onclick: () => n.remove() }, "Close"));
    } catch (e) {
      notice("bad result", el("pre", { class: "grow" }, e.message));
    }
    await loadQueue();
  }

  pushButton.addEventListener("click", askPush);
  for (const b of document.querySelectorAll("[data-cancel]")) b.addEventListener("click", () => b.closest("dialog").close("cancel"));

  // ---------- Browse ----------

  async function loadTree() {
    if (!state.tree) state.tree = await api("/api/tree");
    return state.tree;
  }

  const OPENED_KEY = "memcore-browse-open";

  // loadOpened returns the folders that the person opened in Browse. They are a convenience only.
  function loadOpened() {
    try {
      const v = JSON.parse(localStorage.getItem(OPENED_KEY) || "[]");
      return new Set(Array.isArray(v) ? v.filter((p) => typeof p === "string") : []);
    } catch (e) {
      return new Set();
    }
  }

  function saveOpened(opened) {
    try {
      localStorage.setItem(OPENED_KEY, JSON.stringify([...opened]));
    } catch (e) {
      /* the opened folders are a convenience only */
    }
  }

  // BrowseView shows the tree of main beside a document. target is a path, or a path and a line as path:line.
  // The folders above the document open, so the tree reveals it.
  function BrowseView({ target }) {
    let wanted = target || "README.md";
    let line = 0;
    const m = /^(.*):(\d+)$/.exec(wanted);
    if (m) {
      wanted = m[1];
      line = Number(m[2]);
    }
    const [tree, setTree] = useState(state.tree);
    const [doc, setDoc] = useState(null);
    const [error, setError] = useState("");
    const [opened, setOpened] = useState(loadOpened);
    const [picked, setView] = useState(null); // the view that the person picked, if any
    useEffect(() => {
      let live = true;
      (async () => {
        try {
          const t = await loadTree();
          if (!live) return;
          setTree(t);
          let path = wanted;
          if (!t.docs.some((d) => d.path === path) && t.docs.some((d) => d.path === path + "/README.md")) path += "/README.md";
          const d = await api("/api/doc?path=" + encodeURIComponent(path));
          if (live) setDoc(d);
        } catch (e) {
          if (live) setError(e.message);
        }
      })();
      return () => {
        live = false;
      };
    }, []);
    const current = doc ? doc.path : wanted;
    useEffect(() => {
      const above = ancestors(current).filter((p) => !opened.has(p));
      if (!above.length) return;
      const next = new Set([...opened, ...above]);
      saveOpened(next);
      setOpened(next);
    }, [current]);
    const toggle = (folder) => {
      const next = new Set(opened);
      if (next.has(folder)) next.delete(folder);
      else next.add(folder);
      saveOpened(next);
      setOpened(next);
    };
    if (!tree && !error) return html`<p class="muted">Reading the documents.</p>`;
    let body;
    if (error) body = html`<p class="notice bad">${error}</p>`;
    else if (!doc) body = html`<p class="muted">Reading the document.</p>`;
    else {
      const markdown = isMarkdown(doc.path);
      // The view depends on the resolved path: #browse/dev opens dev/README.md, rendered.
      const view = picked || (markdown && !line ? "rendered" : "source");
      body = html`<div class="doc-head">
          <span class="path">${doc.path}</span>
          ${doc.unreviewed ? html`<span class="tag unreviewed">unreviewed</span>` : null}
          ${markdown
            ? html`<${Seg} label="View of the document" views=${[["rendered", "Rendered"], ["source", "Source"]]} value=${view} onChange=${setView} />`
            : null}
        </div>
        ${markdown && view === "rendered" ? html`<${Rendered} text=${doc.text} path=${doc.path} />` : html`<${Source} text=${doc.text} line=${line} />`}`;
    }
    return html`<div class="browse">
      ${tree ? html`<${Tree} docs=${tree.docs} current=${current} opened=${opened} onToggle=${toggle} />` : html`<div></div>`}
      <article>${body}</article>
    </div>`;
  }

  // ---------- Search ----------

  let searchTimer = 0;

  // highlight marks each match of the query in a line, as text.
  function highlight(text, query) {
    const out = [];
    const lower = text.toLowerCase();
    const q = query.toLowerCase();
    let at = 0;
    for (let i = lower.indexOf(q); q && i >= 0; i = lower.indexOf(q, i + q.length)) {
      out.push(text.slice(at, i), el("mark", {}, text.slice(i, i + q.length)));
      at = i + q.length;
    }
    out.push(text.slice(at));
    return out;
  }

  function showSearch(query) {
    const input = el("input", { type: "search", placeholder: "Search the text of every document", "aria-label": "Search", value: query || "" });
    const results = el("div", { class: "hits" });
    const run = async () => {
      const q = input.value;
      history.replaceState(null, "", "#search/" + encodeURIComponent(q));
      if (!q.trim()) {
        results.replaceChildren();
        return;
      }
      let h;
      try {
        h = await api("/api/search?q=" + encodeURIComponent(q));
      } catch (e) {
        results.replaceChildren(el("p", { class: "notice bad" }, e.message));
        return;
      }
      if (q !== input.value) return; // a newer search runs
      if (!h.hits.length) {
        results.replaceChildren(el("p", { class: "muted" }, "No document holds this text."));
        return;
      }
      const groups = new Map();
      for (const hit of h.hits) {
        if (!groups.has(hit.path)) groups.set(hit.path, []);
        groups.get(hit.path).push(hit);
      }
      const parts = [el("p", { class: "muted" }, `${plural(h.hits.length, "line")} in ${plural(groups.size, "document")}.` + (h.more ? " The search stopped at its limit." : ""))];
      for (const [path, hits] of groups) {
        parts.push(el("div", { class: "hit-file" }, path));
        for (const hit of hits) {
          parts.push(el("a", { href: "#browse/" + encodeURIComponent(path + ":" + hit.line) }, el("span", { class: "ln" }, hit.line), el("span", {}, highlight(hit.text, q))));
        }
      }
      results.replaceChildren(...parts);
    };
    input.addEventListener("input", () => {
      clearTimeout(searchTimer);
      searchTimer = setTimeout(run, 250);
    });
    plain();
    main.replaceChildren(input, results);
    input.focus();
    if (query) run();
  }

  // ---------- Routing and keys ----------

  // browseVisit changes to mount Browse again on the same target.
  let browseVisit = 0;

  function route() {
    const hash = location.hash.slice(1);
    const [view, ...rest] = hash.split("/");
    const arg = decodeURIComponent(rest.join("/"));
    for (const a of document.querySelectorAll(".tabs a")) a.classList.toggle("on", a.dataset.view === (view || "review"));
    pushButton.hidden = true;
    if (view === "browse") mount(html`<${BrowseView} key=${arg + "\n" + browseVisit} target=${arg} />`);
    else if (view === "search") showSearch(arg);
    else if (state.queue && (!state.head || state.queue.main === state.head)) renderReview();
    else loadQueue();
  }

  document.addEventListener("keydown", (e) => {
    if (e.metaKey || e.ctrlKey || e.altKey || document.querySelector("dialog[open]")) return;
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName);
    if (e.key === "/" && !typing) {
      e.preventDefault();
      location.hash = "#search";
      return;
    }
    if (typing || !location.hash.startsWith("#review") || !state.entries) return;
    const boxes = main.querySelectorAll(".entry");
    if (e.key === "j") select(state.selected + 1, true);
    else if (e.key === "k") select(state.selected - 1, true);
    else if (e.key === "Enter" && boxes[state.selected]) boxes[state.selected].classList.toggle("collapsed");
    else if (e.key === "d" && state.entries[state.selected] && state.entries[state.selected].commit) askDrop(state.entries[state.selected]);
    else if (e.key === "p") askPush();
    else return;
    e.preventDefault();
  });

  // The event stream keeps the server alive, and tells the page when the queue changes. The view changes only
  // when the person loads the new queue, so a push sends only what the person saw.
  function listen() {
    const events = new EventSource("/api/events?token=" + encodeURIComponent(token));
    events.addEventListener("head", (e) => {
      clearNotices("stopped");
      changed(e.data);
    });
    events.addEventListener("queue", (e) => changed(e.data));
    events.onerror = () => {
      if (events.readyState === EventSource.CLOSED || !notices.querySelector(".stopped")) {
        clearNotices("stopped");
        notice("bad stopped", el("span", { class: "grow" }, "The window lost its server. Run memcore ui to open a new window."));
      }
    };
  }

  function changed(head) {
    if (head !== state.head) state.tree = null;
    state.head = head;
    if (state.queue && head !== state.queue.main) showChanged();
  }

  function showChanged() {
    clearNotices("queue");
    notice(
      "queue",
      el("span", { class: "grow" }, "New commits changed the queue. This window still shows the queue that you see, and a push sends only that one."),
      el("button", { type: "button", onclick: () => (location.hash === "#review" ? loadQueue() : (location.hash = "#review")) }, "Load the new queue")
    );
  }

  if (!token) {
    main.replaceChildren(el("p", {}, "This window has no token. Run memcore ui to open a window."));
    return;
  }
  window.addEventListener("hashchange", route);
  listen();
  route();
})();
