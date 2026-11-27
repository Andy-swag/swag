(() => {
  "use strict";

  const idNum = (id) => {
    if (!id) return 0;
    const m = /(\d+)$/.exec(String(id));
    return m ? +m[1] : 0;
  };

  // Fast non-cryptographic 32-bit FNV-1a hash for O(1) DOM dirty checking.
  function hashStr(s) {
    let h = 2166136261;
    for (let i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i);
      h = Math.imul(h, 16777619);
    }
    return (h >>> 0).toString(36);
  }

  function nodeKey(el, idx) {
    const dk = el.dataset?.k;
    if (dk) return dk;
    const mid = el.dataset?.id || el.querySelector?.(".msg[data-id]")?.dataset?.id;
    if (mid) return "m:" + mid;
    if (el.classList?.contains("unread-sep")) return "unread-sep";
    if (el.classList?.contains("earlier-row")) return "earlier-row";
    if (el.classList?.contains("needs")) return "needs";
    if (el.classList?.contains("hello")) return "hello";
    return `${el.tagName}:${el.className || ""}:${idx}`;
  }

  // Incremental keyed DOM patcher: preserves untouched nodes, images, text
  // selections, focus, and open <details> / expanded folds across SSE ticks.
  const patchTpl = document.createElement("template");
  function patchHTML(container, html) {
    if (!container) return false;
    patchTpl.innerHTML = html;
    const nextEls = [...patchTpl.content.children];
    if (!nextEls.length) {
      if (container.childElementCount === 0) return false;
      container.innerHTML = "";
      return true;
    }

    const oldMap = new Map();
    const usedKeys = new Map();
    for (const child of [...container.children]) {
      const k = child._swagKey || nodeKey(child, 0);
      if (!oldMap.has(k)) oldMap.set(k, child);
    }

    let changed = false;
    const nextNodes = [];
    for (let i = 0; i < nextEls.length; i++) {
      const cand = nextEls[i];
      let k = nodeKey(cand, i);
      const seen = usedKeys.get(k) || 0;
      usedKeys.set(k, seen + 1);
      if (seen > 0) k = `${k}#${seen}`;
      const sig = hashStr(cand.outerHTML);
      const existing = oldMap.get(k);
      if (existing) {
        oldMap.delete(k);
        if (existing._swagHash === sig) {
          nextNodes.push(existing);
          continue;
        }
        // Preserve any expanded fold toggle inside the node, and suppress
        // re-playing entrance animations when patching an existing .ask card.
        const wasUnfolded = existing.classList?.contains("is-unfolded");
        cand._swagKey = k;
        cand._swagHash = sig;
        if (wasUnfolded) cand.classList.add("is-unfolded");
        if (cand.classList?.contains("ask") || cand.classList?.contains("needs")) {
          cand.querySelectorAll?.(".ask").forEach((a) => a.classList.add("no-rise"));
          if (cand.classList.contains("ask")) cand.classList.add("no-rise");
        }
        nextNodes.push(cand);
        changed = true;
      } else {
        cand._swagKey = k;
        cand._swagHash = sig;
        nextNodes.push(cand);
        changed = true;
      }
    }

    if (oldMap.size > 0) {
      for (const stale of oldMap.values()) stale.remove();
      changed = true;
    }

    // Reconcile child ordering with minimal DOM mutations.
    for (let i = 0; i < nextNodes.length; i++) {
      const want = nextNodes[i];
      const cur = container.children[i];
      if (cur !== want) {
        container.insertBefore(want, cur || null);
        changed = true;
      }
    }
    while (container.childElementCount > nextNodes.length) {
      container.lastElementChild.remove();
      changed = true;
    }
    return changed;
  }

  // ───────────────────────────── Cross-device read cursor & unread divider
  const readState = {
    serverCursor: "",
    dividerAfter: "",
    lastSent: "",
    timer: 0,
  };

  function latestTalkEntry(entries) {
    for (let i = entries.length - 1; i >= 0; i--) {
      const e = entries[i];
      if (e?.id && (e.role === "swag" || e.role === "user")) return e;
    }
    return null;
  }

  function countUnreadAfter(entries, cursorId) {
    const cn = idNum(cursorId);
    if (!cn) return 0;
    let n = 0;
    for (let i = entries.length - 1; i >= 0; i--) {
      const e = entries[i];
      if (!e?.id) continue;
      if (idNum(e.id) <= cn) break;
      if (e.role === "swag") n++;
    }
    return n;
  }

  function updateServerCursor(cursor, entries) {
    if (cursor && idNum(cursor) > idNum(readState.serverCursor)) {
      readState.serverCursor = cursor;
    }
    if (!readState.dividerAfter && readState.serverCursor && countUnreadAfter(entries, readState.serverCursor) > 0) {
      readState.dividerAfter = readState.serverCursor;
    }
  }

  function unreadDividerAfter(entries) {
    if (!readState.dividerAfter) return "";
    return countUnreadAfter(entries, readState.dividerAfter) > 0 ? readState.dividerAfter : "";
  }

  function markReadAtBottom(entries, api, immediate = false) {
    const last = latestTalkEntry(entries);
    if (!last) return;
    if (idNum(last.id) <= idNum(readState.serverCursor) && idNum(last.id) <= idNum(readState.lastSent)) return;
    readState.serverCursor = last.id;
    clearTimeout(readState.timer);
    const send = () => {
      if (idNum(last.id) <= idNum(readState.lastSent)) return;
      readState.lastSent = last.id;
      api("/api/super/read", { body: { cursor: last.id } }).catch(() => {});
    };
    if (immediate) send();
    else readState.timer = setTimeout(send, 450);
  }

  function clearUnreadDivider() {
    readState.dividerAfter = "";
  }

  function syncJumpBadge(btn, entries, isAtBottom, api) {
    if (!btn) return;
    if (isAtBottom) {
      markReadAtBottom(entries, api, false);
    }
    const n = countUnreadAfter(entries, readState.serverCursor);
    let badge = btn.querySelector(".jump-badge");
    if (n > 0) {
      if (!badge) {
        badge = document.createElement("span");
        badge.className = "jump-badge";
        btn.appendChild(badge);
      }
      const txt = n > 99 ? "99+" : String(n);
      if (badge.textContent !== txt) badge.textContent = txt;
      btn.classList.add("has-unread");
    } else {
      badge?.remove();
      btn.classList.remove("has-unread");
    }
  }

  // ───────────────────────────── Debounced busy indicator
  let busyTimer = 0;
  function syncWorkingRow(wk, busy, cls, html) {
    if (!wk) return;
    if (busy) {
      clearTimeout(busyTimer);
      wk.className = cls;
      if (wk._busyHtml !== html) {
        wk._busyHtml = html;
        wk.innerHTML = html;
      }
      wk.hidden = false;
    } else if (!wk.hidden) {
      clearTimeout(busyTimer);
      busyTimer = setTimeout(() => {
        wk.hidden = true;
      }, 180);
    }
  }

  // ───────────────────────────── Universal Focus Topic Drawer
  let ctx = null;
  let drawer = {
    open: false,
    focusId: "",
    rootId: "",
    wide: false,
    unfolded: new Set(),
    el: null,
    back: null,
  };

  const ICON_FOCUS = `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 6h16M4 12h10M4 18h13"/><circle cx="19" cy="12" r="2.2"/></svg>`;

  function buildTopicGraph(entries, seedId) {
    const byId = new Map();
    const children = new Map();
    for (const e of entries) {
      if (e?.id) byId.set(e.id, e);
      const pid = e?.re?.entry;
      if (pid) {
        let arr = children.get(pid);
        if (!arr) children.set(pid, (arr = []));
        arr.push(e);
      }
    }
    let root = byId.get(seedId);
    if (!root) return { root: null, items: [], workers: new Set() };
    const visited = new Set([root.id]);
    while (root.re?.entry && byId.has(root.re.entry) && !visited.has(root.re.entry)) {
      root = byId.get(root.re.entry);
      visited.add(root.id);
    }

    // BFS collect all entries connected to root via re.entry.
    const ids = new Set([root.id]);
    const queue = [root.id];
    const workers = new Set();
    if (root.re?.worker) workers.add(root.re.worker);
    while (queue.length) {
      const cur = queue.shift();
      for (const ch of children.get(cur) || []) {
        if (ch.id && !ids.has(ch.id)) {
          ids.add(ch.id);
          queue.push(ch.id);
          if (ch.re?.worker) workers.add(ch.re.worker);
        }
      }
    }
    // Also pull direct replies immediately following root if root has no explicit children yet.
    const rootIdx = entries.findIndex((e) => e?.id === root.id);
    if (ids.size === 1 && rootIdx >= 0) {
      for (let i = rootIdx + 1; i < Math.min(entries.length, rootIdx + 6); i++) {
        const nxt = entries[i];
        if (!nxt) continue;
        if (nxt.role === "user" && !nxt.re?.entry) break;
        if (nxt.role === "swag" && (!nxt.re?.entry || nxt.re.entry === root.id)) {
          if (nxt.id) ids.add(nxt.id);
          if (nxt.re?.worker) workers.add(nxt.re.worker);
          break;
        }
      }
    }
    // Collect mentioned workers from event lines or text refs.
    for (const e of entries) {
      if (ids.has(e?.id) && e.ref) workers.add(e.ref);
    }
    const minAt = root.at ? Date.parse(root.at) - 60e3 : 0;
    const items = entries.filter((e) => {
      if (!e) return false;
      if (e.id && ids.has(e.id)) return true;
      if (e.role === "event" && e.ref && workers.has(e.ref) && (!minAt || Date.parse(e.at) >= minAt)) return true;
      return false;
    });
    return { root, items, workers };
  }

  function isLongReport(e) {
    if (!e || e.role !== "swag") return false;
    const t = String(e.text || "");
    if (t.length > 850) return true;
    let lines = 1;
    for (let i = 0; i < t.length; i++) if (t.charCodeAt(i) === 10 && ++lines > 14) return true;
    return false;
  }

  function ensureDrawer() {
    if (drawer.el) return drawer.el;
    const host = document.querySelector("#v-swag");
    if (!host) return null;

    const back = document.createElement("div");
    back.className = "td-back";
    back.hidden = true;

    const el = document.createElement("aside");
    el.className = "td-drawer";
    el.id = "td-drawer";
    el.hidden = true;
    el.innerHTML = `
      <header class="td-head">
        <div class="td-title-wrap">
          <div class="td-title" id="td-title">话题专注视图</div>
          <div class="td-meta muted" id="td-meta"></div>
        </div>
        <div class="td-actions">
          <button type="button" class="btn ghost small" id="td-widen" title="切换 50/50 并排宽屏">50/50 宽屏</button>
          <button type="button" class="btn ghost small" id="td-jump-main" title="在主对话中定位">主对话定位</button>
          <button type="button" class="icon-btn" id="td-close" title="关闭 (Esc)" aria-label="关闭">
            <svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6L6 18"/></svg>
          </button>
        </div>
      </header>
      <div class="td-body" id="td-body"><div class="thread td-thread" id="td-thread"></div></div>
      <form class="composer td-composer" id="td-composer">
        <div class="cbox">
          <div class="files" hidden></div>
          <textarea id="td-input" rows="1" placeholder="在该话题下继续回复（支持粘贴截图 / 拖拽文件）…" enterkeyhint="send"></textarea>
          <div class="cbar">
            <button type="button" class="icon-btn attach" title="添加图片或文件"><svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg></button>
            <span class="muted td-hint">回复自动关联当前话题</span>
            <span class="spacer"></span>
            <button class="send" type="submit" title="发送 (Enter)"><svg viewBox="0 0 24 24"><path d="M12 19V5M5 12l7-7 7 7"/></svg></button>
          </div>
        </div>
        <input type="file" multiple hidden>
      </form>`;

    host.appendChild(back);
    host.appendChild(el);
    drawer.el = el;
    drawer.back = back;

    back.addEventListener("click", closeTopic);
    el.querySelector("#td-close").addEventListener("click", closeTopic);
    el.querySelector("#td-widen").addEventListener("click", () => {
      drawer.wide = !drawer.wide;
      localStorage.setItem("swag:tdWide", drawer.wide ? "1" : "0");
      applyWide();
    });
    el.querySelector("#td-jump-main").addEventListener("click", () => {
      const id = drawer.focusId || drawer.rootId;
      if (id) ctx?.jumpToEntry?.(id);
    });
    el.querySelector("#td-thread").addEventListener("click", (ev) => {
      const foldBtn = ev.target.closest("[data-td-fold]");
      if (foldBtn) {
        const id = foldBtn.dataset.tdFold;
        if (drawer.unfolded.has(id)) drawer.unfolded.delete(id);
        else drawer.unfolded.add(id);
        renderTopic();
        return;
      }
      const optBtn = ev.target.closest(".reply-opt[data-opt]");
      if (optBtn) {
        const grp = optBtn.closest(".reply-opts[data-entry]");
        if (!grp || grp.dataset.busy) return;
        grp.dataset.busy = "1";
        const id = grp.dataset.entry, text = optBtn.dataset.opt;
        const e = ctx?.chatEntries?.().find((x) => x.id === id);
        ctx?.sendSuper?.(text, e ? { entry: e.id, who: "Swag", text: e.text } : { entry: id, who: "Swag", text: "" }, false, { keepScroll: true });
        return;
      }
      const ackBtn = ev.target.closest(".reply-ack[data-ack]");
      if (ackBtn) {
        ev.preventDefault();
        ctx?.dismissPending?.(ackBtn.dataset.ack);
      }
    });

    if (ctx?.bindComposer) {
      ctx.bindComposer(el.querySelector("#td-composer"), el.querySelector("#td-input"), async (text) => {
        const entries = ctx.chatEntries();
        const graph = buildTopicGraph(entries, drawer.rootId || drawer.focusId);
        const lastTalk = [...graph.items].reverse().find((x) => x.role === "swag" || x.role === "user") || graph.root;
        const re = lastTalk ? { entry: lastTalk.id, who: lastTalk.role === "user" ? "你" : "Swag", text: lastTalk.text } : null;
        await ctx.sendSuper(text, re, false, { keepScroll: true });
        renderTopic(true);
      }, () => ctx.getTargetMachine?.() || "");
    }

    drawer.wide = localStorage.getItem("swag:tdWide") === "1";
    return el;
  }

  function applyWide() {
    const host = document.querySelector("#v-swag");
    if (!drawer.el || !host) return;
    const isWide = drawer.open && drawer.wide && window.innerWidth >= 1024;
    drawer.el.classList.toggle("wide", isWide);
    host.classList.toggle("td-split", isWide);
    const btn = drawer.el.querySelector("#td-widen");
    if (btn) btn.textContent = drawer.wide ? "收窄抽屉" : "50/50 宽屏";
  }

  function renderTopic(scrollBottom = false) {
    if (!drawer.open || !drawer.el || !ctx) return;
    const entries = ctx.chatEntries();
    const { root, items, workers } = buildTopicGraph(entries, drawer.focusId || drawer.rootId);
    if (!root) {
      drawer.el.querySelector("#td-title").textContent = "未找到该话题";
      drawer.el.querySelector("#td-meta").textContent = "";
      patchHTML(drawer.el.querySelector("#td-thread"), `<div class="thread-empty">这条消息可能还没加载，先在主对话向上翻找。</div>`);
      return;
    }
    drawer.rootId = root.id;
    const title = ctx.plainLine(root.text || "").slice(0, 48) || "话题专注视图";
    drawer.el.querySelector("#td-title").textContent = title;
    const wList = [...workers].filter(Boolean);
    drawer.el.querySelector("#td-meta").textContent = `${items.length} 条记录` + (wList.length ? ` · 涉及 ${wList.join("、")}` : "");

    const body = drawer.el.querySelector("#td-body");
    const atBottom = scrollBottom || (body.scrollHeight - body.scrollTop - body.clientHeight <= 48);
    const esc = ctx.esc;
    const html = items.map((e) => {
      const k = esc("td:" + (e.id || e.at));
      const at = +new Date(e.at) || 0;
      const tStr = at ? `<span class="td-time muted">${esc(ctx.sepTime(at))}</span>` : "";
      if (e.role === "user") {
        return `<div class="mine td-item" data-k="${k}"><div class="msg user" data-id="${esc(e.id || "")}">${ctx.userText(e.text)}</div><div class="td-foot">${tStr}</div></div>`;
      }
      if (e.role === "swag") {
        const long = isLongReport(e);
        const open = !long || drawer.unfolded.has(e.id);
        const foldBtn = long
          ? `<button type="button" class="td-fold-btn" data-td-fold="${esc(e.id)}">${open ? "收起长回报" : "展开完整内容"}</button>`
          : "";
        return `<div class="say td-item${long && !open ? " is-folded" : ""}" data-k="${k}">
          <span class="avatar"><img src="/icon.svg" alt=""></span>
          <div class="msg assistant" data-id="${esc(e.id || "")}">
            <div class="td-msg-content">${ctx.md(e.text)}${ctx.imagesIn(e.text)}</div>
            ${foldBtn}
            <div class="td-foot">${tStr}</div>
          </div></div>`;
      }
      return `<div class="event td-ev" data-k="${k}"><span class="dot ${e.tone === "error" ? "failed" : e.tone === "done" ? "done" : "unknown"}"></span><span class="when">${esc(ctx.hhmm(e.at))}</span><span>${esc(e.text)}</span></div>`;
    }).join("");

    patchHTML(drawer.el.querySelector("#td-thread"), html);
    if (atBottom) body.scrollTop = body.scrollHeight;
  }

  function openTopic(entryId) {
    if (!entryId || !ctx) return;
    ensureDrawer();
    drawer.open = true;
    drawer.focusId = entryId;
    drawer.rootId = "";
    drawer.el.hidden = false;
    drawer.back.hidden = false;
    applyWide();
    renderTopic(true);
    setTimeout(() => drawer.el?.querySelector("#td-input")?.focus({ preventScroll: true }), 40);
  }

  function closeTopic() {
    if (!drawer.open) return;
    drawer.open = false;
    if (drawer.el) drawer.el.hidden = true;
    if (drawer.back) drawer.back.hidden = true;
    applyWide();
  }

  function init(hooks) {
    ctx = hooks;
    document.addEventListener("click", (ev) => {
      const btn = ev.target.closest("[data-focus-topic]");
      if (btn) {
        ev.preventDefault();
        openTopic(btn.dataset.focusTopic);
      }
    });
    document.addEventListener("keydown", (ev) => {
      if (ev.key === "Escape" && drawer.open && !document.querySelector("dialog[open]")) {
        ev.preventDefault();
        closeTopic();
      }
    });
    window.addEventListener("resize", () => { if (drawer.open) applyWide(); });
  }

  const focusActBtn = (id, esc) =>
    `<button type="button" class="focus-btn" data-focus-topic="${esc(id)}" title="在话题抽屉中专注查看" aria-label="专注话题">${ICON_FOCUS}</button>`;

  // Patch an existing .ask dialog box in-place so ticking screen updates never
  // destroy the wrapper or replay its CSS entrance animation ("rise").
  function patchAskBox(box, html) {
    if (!box) return;
    const curAsk = box.firstElementChild?.classList?.contains("ask") ? box.firstElementChild : null;
    if (curAsk && html) {
      patchTpl.innerHTML = html;
      const nextAsk = patchTpl.content.firstElementChild;
      if (nextAsk?.classList?.contains("ask")) {
        const prevCtxTop = curAsk.querySelector(".ctx")?.scrollTop || 0;
        const prevRawTop = curAsk.querySelector(".ask-raw")?.scrollTop || 0;
        curAsk.classList.add("no-rise");
        curAsk.innerHTML = nextAsk.innerHTML;
        const ctxEl = curAsk.querySelector(".ctx");
        if (ctxEl && prevCtxTop) ctxEl.scrollTop = prevCtxTop;
        const rawEl = curAsk.querySelector(".ask-raw");
        if (rawEl && prevRawTop) rawEl.scrollTop = prevRawTop;
        return;
      }
    }
    box.innerHTML = html;
  }

  window.SwagChat = {
    init,
    patchHTML,
    patchAskBox,
    updateServerCursor,
    unreadDividerAfter,
    markReadAtBottom,
    clearUnreadDivider,
    syncJumpBadge,
    syncWorkingRow,
    openTopic,
    closeTopic,
    refreshTopic: () => renderTopic(false),
    isTopicOpen: () => drawer.open,
    focusActBtn,
  };
})();
