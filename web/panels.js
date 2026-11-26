(() => {
  const fmtTok = (n) => {
    n = Number(n || 0);
    if (n >= 1e9) return (n / 1e9).toFixed(2) + "B";
    if (n >= 1e6) return (n / 1e6).toFixed(2) + "M";
    if (n >= 1e3) return (n / 1e3).toFixed(1) + "K";
    return String(n);
  };
  const pct = (a, b) => (b > 0 ? Math.round((a / b) * 100) + "%" : "—");
  const agoShort = (iso) => {
    if (!iso || String(iso).startsWith("0001-01-01")) return "";
    const ts = Date.parse(iso);
    if (!Number.isFinite(ts) || ts <= 0) return "";
    const s = (Date.now() - ts) / 1000;
    if (!(s >= 0) || s < 90) return "刚刚";
    if (s < 3600) return Math.round(s / 60) + " 分钟前";
    if (s < 86400) return Math.round(s / 3600) + " 小时前";
    return Math.round(s / 86400) + " 天前";
  };
  const fmtReset = (unixSec, mins) => {
    if (!unixSec) return "";
    const d = new Date(unixSec * 1000);
    const p2 = (v) => String(v).padStart(2, "0");
    return mins >= 1440
      ? `${p2(d.getMonth() + 1)}/${p2(d.getDate())} 重置`
      : `${p2(d.getHours())}:${p2(d.getMinutes())} 重置`;
  };
  const barColor = (rem) => (rem >= 50 ? "#22c55e" : rem >= 20 ? "#f59e0b" : "#ef4444");

  function openConfirmModal({ title, message, confirmText = "确定", danger = false }, esc) {
    return new Promise((resolve) => {
      const bg = document.createElement("div");
      bg.className = "pool-modal-bg";
      bg.innerHTML = `<div class="pool-modal compact" role="dialog" aria-modal="true">
        <div class="pool-modal-head">
          <h3>${esc(title || "请确认")}</h3>
          <button type="button" class="pool-ico-btn" data-cm-no title="关闭">✕</button>
        </div>
        <div class="pool-modal-body"><p class="pool-confirm-msg">${esc(message || "")}</p></div>
        <div class="pool-modal-foot">
          <span class="grow"></span>
          <button type="button" class="btn small" data-cm-no>取消</button>
          <button type="button" class="btn ${danger ? "danger" : "primary"} small" id="cm-yes">${esc(confirmText)}</button>
        </div>
      </div>`;
      const finish = (v) => { document.removeEventListener("keydown", onKey); bg.remove(); resolve(v); };
      const onKey = (ev) => {
        if (ev.key === "Escape") finish(false);
        else if (ev.key === "Enter" && !ev.isComposing) { ev.preventDefault(); finish(true); }
      };
      document.addEventListener("keydown", onKey);
      document.body.appendChild(bg);
      bg.querySelector("#cm-yes")?.focus();
      bg.addEventListener("click", (ev) => {
        if (ev.target === bg || ev.target.closest("[data-cm-no]")) finish(false);
        else if (ev.target.closest("#cm-yes")) finish(true);
      });
    });
  }

  function openInputModal({ title, label, placeholder = "", confirmText = "确定" }, esc) {
    return new Promise((resolve) => {
      const bg = document.createElement("div");
      bg.className = "pool-modal-bg";
      bg.innerHTML = `<div class="pool-modal compact" role="dialog" aria-modal="true">
        <div class="pool-modal-head">
          <h3>${esc(title || "请输入")}</h3>
          <button type="button" class="pool-ico-btn" data-im-no title="关闭">✕</button>
        </div>
        <div class="pool-modal-body">
          <label class="pool-field">${esc(label || "")}<input data-im-val class="mono" placeholder="${esc(placeholder)}"></label>
        </div>
        <div class="pool-modal-foot">
          <span class="grow"></span>
          <button type="button" class="btn small" data-im-no>取消</button>
          <button type="button" class="btn primary small" id="im-yes">${esc(confirmText)}</button>
        </div>
      </div>`;
      const inp = bg.querySelector("[data-im-val]");
      const finish = (v) => { document.removeEventListener("keydown", onKey); bg.remove(); resolve(v); };
      const onKey = (ev) => {
        if (ev.key === "Escape") finish("");
        else if (ev.key === "Enter" && !ev.isComposing) { ev.preventDefault(); finish((inp?.value || "").trim()); }
      };
      document.addEventListener("keydown", onKey);
      document.body.appendChild(bg);
      inp?.focus();
      bg.addEventListener("click", (ev) => {
        if (ev.target === bg || ev.target.closest("[data-im-no]")) finish("");
        else if (ev.target.closest("#im-yes")) finish((inp?.value || "").trim());
      });
    });
  }

  async function renderUsage(body, api, esc, toast, days = 7, fresh = false) {
    const gen = body._autoGen;
    body.innerHTML = `<p class="panel-intro">正在聚合所有在线机器的 Token 使用数据…</p>`;
    let u;
    try {
      u = await api(`/api/super/usage?days=${days}${fresh ? "&fresh=1" : ""}`);
    } catch (err) {
      if (body._autoGen !== gen) return;
      body.innerHTML = `<p class="panel-intro">读取用量失败：${esc(err.message)}</p>`;
      return;
    }
    if (body._autoGen !== gen) return;
    const tot = u.total || {}, tod = u.today || {};
    const daily = u.daily || [];
    const maxDay = Math.max(1, ...daily.map((d) => d.total || 0));
    const machRows = Object.entries(u.by_machine || {}).sort((a, b) => (b[1].total || 0) - (a[1].total || 0));
    const modelRows = Object.entries(u.by_model || {}).sort((a, b) => (b[1].total || 0) - (a[1].total || 0));
    const poolRows = Object.entries(u.pool_stats || {});
    const errs = Object.entries(u.errors || {});

    const barRow = (label, b, maxVal) => {
      const w = Math.max(2, Math.round(((b.total || 0) / Math.max(1, maxVal)) * 100));
      return `<li class="row" style="gap:10px;align-items:center">
        <span class="mono" style="min-width:130px;font-size:12px">${esc(label)}</span>
        <div class="grow" style="background:var(--bg-3);border-radius:6px;height:10px;overflow:hidden">
          <div style="width:${w}%;height:100%;background:var(--fg);border-radius:6px"></div>
        </div>
        <span class="mono" style="min-width:165px;text-align:right;font-size:12px"><b>${fmtTok(b.total)}</b> <span class="muted">(${b.sessions || 0} 会话 · 缓存 ${pct(b.cached, b.input)})</span></span>
      </li>`;
    };

    body.innerHTML = `<p class="panel-intro">跨机器汇总 Codex 与 Claude 会话日志中的真实 Token 消耗（含缓存命中率），每日汇总自动持久化到 Hub SQLite，便于观察成本与优化模型分级。</p>
      <div style="display:flex;gap:8px;align-items:center;margin-bottom:12px">
        <div class="seg">
          <button type="button" data-usage-days="7" class="${days === 7 ? "on" : ""}">近 7 天</button>
          <button type="button" data-usage-days="30" class="${days === 30 ? "on" : ""}">近 30 天</button>
        </div>
        <button type="button" class="btn small" data-usage-refresh="${days}">刷新</button>
        ${errs.length ? `<span class="muted" style="font-size:12px">离线/跳过：${esc(errs.map(([k, v]) => `${k}(${v})`).join("、"))}</span>` : ""}
      </div>
      <div class="set-grid" style="grid-template-columns:repeat(auto-fit,minmax(145px,1fr));margin-bottom:14px">
        <div class="card" style="padding:12px"><div class="muted" style="font-size:12px">今日 Token</div><div style="font-size:22px;font-weight:700;margin-top:4px">${fmtTok(tod.total)}</div><div class="muted" style="font-size:11px">输入 ${fmtTok(tod.input)} · 输出 ${fmtTok(tod.output)}</div></div>
        <div class="card" style="padding:12px"><div class="muted" style="font-size:12px">近 ${days} 天总量</div><div style="font-size:22px;font-weight:700;margin-top:4px">${fmtTok(tot.total)}</div><div class="muted" style="font-size:11px">输入 ${fmtTok(tot.input)} · 输出 ${fmtTok(tot.output)}</div></div>
        <div class="card" style="padding:12px"><div class="muted" style="font-size:12px">缓存命中率</div><div style="font-size:22px;font-weight:700;margin-top:4px">${pct(tot.cached, tot.input)}</div><div class="muted" style="font-size:11px">命中 ${fmtTok(tot.cached)} tokens</div></div>
        <div class="card" style="padding:12px"><div class="muted" style="font-size:12px">活跃会话数</div><div style="font-size:22px;font-weight:700;margin-top:4px">${tot.sessions || 0}</div><div class="muted" style="font-size:11px">Codex ${u.by_kind?.codex?.sessions || 0} · Claude ${u.by_kind?.claude?.sessions || 0}</div></div>
      </div>
      <div class="card"><div class="card-h">按日趋势</div><ul class="rows">${daily.length ? daily.slice().reverse().map((d) => barRow(d.date, d, maxDay)).join("") : `<li class="empty-row">暂无统计数据</li>`}</ul></div>
      <div class="card"><div class="card-h">按机器分布</div><ul class="rows">${machRows.length ? machRows.map(([k, v]) => barRow(k, v, Math.max(1, machRows[0][1].total))).join("") : `<li class="empty-row">暂无</li>`}</ul></div>
      <div class="card"><div class="card-h">按模型分布</div><ul class="rows">${modelRows.length ? modelRows.map(([k, v]) => barRow(k, v, Math.max(1, modelRows[0][1].total))).join("") : `<li class="empty-row">暂无</li>`}</ul></div>
      <div class="card"><div class="card-h">模型池反代通道</div><ul class="rows">${poolRows.length ? poolRows.map(([id, rt]) => `<li class="row"><span class="mono" style="min-width:110px"><b>${esc(id)}</b></span><span class="grow muted">${rt.requests ? `请求 ${rt.requests} · 成功 ${rt.successes}${rt.failovers ? ` · 切换 ${rt.failovers}` : ""}` : "已启用，尚未有对话走此通道（绑定 swag-pool 后生效）"}${rt.last_error ? " · " + esc(rt.last_error) : ""}</span><span class="mono" style="font-size:12px"><b>${fmtTok(rt.total_tokens || 0)}</b> <span class="muted">tokens</span></span></li>`).join("") : `<li class="empty-row">暂无已启用的反代通道</li>`}</ul></div>`;

    body.querySelectorAll("[data-usage-days]").forEach((b) => b.addEventListener("click", () => renderUsage(body, api, esc, toast, +b.dataset.usageDays, false)));
    body.querySelector("[data-usage-refresh]")?.addEventListener("click", (e) => renderUsage(body, api, esc, toast, +e.currentTarget.dataset.usageRefresh, true));
  }

  let poolFilter = "all";
  let poolStatusFilter = "all";
  let poolQuery = "";

  async function renderPool(body, api, esc, toast, fresh = false) {
    const gen = body._autoGen;
    body.innerHTML = `<p class="panel-intro">正在采集各机器原生订阅配额与模型池状态…</p>`;
    let data;
    try {
      data = await api(`/api/super/quota${fresh ? "?fresh=1" : ""}`);
    } catch (err) {
      if (body._autoGen !== gen) return;
      body.innerHTML = `<p class="panel-intro">读取模型池失败：${esc(err.message)}</p>`;
      return;
    }
    if (body._autoGen !== gen) return;
    const subs = data.subscriptions || [];
    const cfg = data.pool || { providers: [], tiers: [] };
    const provs = cfg.providers || [];
    const baseV1 = location.origin + "/v1";
    const codexSnippet = `[model_providers.swag-pool]\nname = "Swag Pool"\nbase_url = "${baseV1}"\nwire_api = "responses"\nsupports_websockets = false`;

    const enabledCount = subs.filter((s) => s.status !== "exhausted").length + provs.filter((p) => p.enabled && !p.cooling).length;
    const pendingCount = provs.filter((p) => !p.enabled).length;
    const coolingCount = subs.filter((s) => s.status === "exhausted").length + provs.filter((p) => p.cooling).length;

    const meterRow = (label, rightText, remPct, mutedBar = false) => {
      const p = Math.max(0, Math.min(100, Number(remPct)));
      const col = mutedBar ? "var(--fg-3)" : barColor(p);
      return `<div class="pool-meter">
        <div class="pool-meter-row">
          <span class="lbl" title="${esc(label)}">${esc(label)}</span>
          <span class="val" style="color:${col}">${esc(rightText)}</span>
        </div>
        <div class="pool-track"><div class="pool-fill" style="width:${p}%;background:${col}"></div></div>
      </div>`;
    };

    const renderModelPills = (models, limit = 4) => {
      if (!Array.isArray(models) || !models.length) return "";
      const shown = models.slice(0, limit);
      const more = models.length - shown.length;
      return `<div class="pool-pills" title="${esc(models.join(" · "))}">${shown.map((m) => `<span class="pool-pill">${esc(m)}</span>`).join("")}${more > 0 ? `<span class="pool-pill">+${more}</span>` : ""}</div>`;
    };

    const subCard = (s) => {
      const badgeCls = s.status === "exhausted" ? "err" : s.status === "low" ? "warn" : "ok";
      const stText = s.status === "exhausted" ? "已耗尽" : s.status === "low" ? "偏低" : "可用";
      const statusBucket = s.status === "exhausted" ? "cooling" : "ready";
      const machs = (Array.isArray(s.machines) && s.machines.length ? s.machines : [s.machine]).filter(Boolean);
      const headTitle = s.account_label ? `${s.title.split("·")[0].trim()} · ${s.account_label}` : s.title;
      const wins = (s.windows || []).map((w) => {
        const rst = fmtReset(w.resets_at, w.window_minutes);
        return meterRow(`${w.label}${rst ? " · " + rst : ""}`, `剩余 ${Math.round(w.remain_percent)}%`, w.remain_percent);
      }).join("");
      const cred = s.credits ? meterRow("Credits 余额", Number(s.credits).toLocaleString(), 100) : "";
      const fallback = !wins && !cred
        ? meterRow(s.models?.length ? `模型族 · ${s.models.length} 组可用` : "原生 CLI 直连", "已就绪", 100, true)
        : "";
      const note = !wins && s.quota_note ? `<div class="pool-subnote">${esc(s.quota_note)}</div>` : "";
      const hay = [s.title, s.account_label, s.plan, s.kind, ...machs, ...(s.models || [])].filter(Boolean).join(" ").toLowerCase();
      return `<div class="pool-card" data-card-cat="sub" data-card-st="${statusBucket}" data-card-q="${esc(hay)}">
        <div class="pool-card-top">
          <div class="pool-head-row">
            <div class="pool-title" title="${esc(headTitle)}">${esc(headTitle)}</div>
          </div>
          <div class="pool-meta-row">
            <span class="pool-status-wrap">
              <span class="pool-badge ${badgeCls}"><span class="bdot"></span>${stText}</span>
              ${s.plan ? `<span class="pool-plan">${esc(s.plan)}</span>` : ""}
            </span>
            ${machs.length ? `<span class="pool-machs" title="${esc(machs.join(" · "))}">${esc(machs.join(" · "))}</span>` : ""}
          </div>
          <div class="pool-meters">${wins}${cred}${fallback}</div>
          ${renderModelPills(s.models, 5)}
          ${note}
        </div>
        <div class="pool-foot">
          <span>最近活动</span>
          <span>${agoShort(s.last_used_at) ? esc(agoShort(s.last_used_at)) + " · " : ""}${s.sessions_7d || 0} 会话</span>
        </div>
      </div>`;
    };

    const modelSelectHTML = (field, curVal, models = []) => {
      const list = [...new Set([...(models || []), ...(curVal ? [curVal] : [])])].filter(Boolean);
      return `<select data-pf="${field}" class="mono">
        <option value="">（透传原模型名）</option>
        ${list.map((m) => `<option value="${esc(m)}" ${m === curVal ? "selected" : ""}>${esc(m)}</option>`).join("")}
        <option value="__custom">其他（手动输入）…</option>
      </select>`;
    };

    const provCard = (p, idx) => {
      const mm = p.model_map || {};
      const rt = p.runtime || {};
      const models = p.cached_models || [];
      const badgeCls = p.cooling ? "err" : p.enabled ? "ok" : "warn";
      const stText = p.cooling ? "冷却中" : p.enabled ? "可用" : "待启用";
      const statusBucket = p.cooling ? "cooling" : p.enabled ? "ready" : "pending";
      const hasQuota = typeof rt.quota_pct === "number";
      const qPct = hasQuota ? rt.quota_pct : (p.cooling ? 0 : p.enabled ? 100 : 0);
      const qLabel = rt.quota_reset ? rt.quota_reset : `API 通道 · ${p.id}`;
      const qRight = hasQuota ? `剩余 ${Math.round(rt.quota_pct)}%` : (p.cooling ? "限流冷却" : p.enabled ? "在线 100%" : "未启用");
      const extraRow = p.enabled && models.length ? meterRow(`已拉取模型`, `${models.length} 个可用`, 100) : "";
      const tierPills = [
        mm["tier-fast"] ? `fast:${mm["tier-fast"]}` : "",
        mm["tier-standard"] ? `std:${mm["tier-standard"]}` : "",
        mm["tier-strong"] ? `strong:${mm["tier-strong"]}` : "",
      ].filter(Boolean);
      const hay = [p.id, p.name, p.kind, p.base_url, ...Object.values(mm), ...models].filter(Boolean).join(" ").toLowerCase();
      const ago = agoShort(rt.last_used_at);
      return `<div class="pool-card" data-card-cat="api" data-card-st="${statusBucket}" data-card-q="${esc(hay)}" data-pool-prov="${idx}">
        <div class="pool-card-top">
          <div class="pool-head-row">
            <div class="pool-title" title="${esc(p.name || p.id)}">${esc(p.name || p.id)}</div>
            <span class="pool-head-acts">
              ${!p.enabled ? `<button type="button" class="btn primary small" style="height:24px;padding:0 8px;font-size:11.5px" data-pool-verify-enable="${idx}">启用</button>` : ""}
              ${p.cooling ? `<button type="button" class="btn small" style="height:24px;padding:0 7px;font-size:11px" data-pool-reset="${esc(p.id)}">解冷</button>` : ""}
              <button type="button" class="pool-ico-btn" data-pool-edit="${idx}" title="编辑配置与模型映射">⚙</button>
            </span>
          </div>
          <div class="pool-meta-row">
            <span class="pool-status-wrap">
              <span class="pool-badge ${badgeCls}"><span class="bdot"></span>${stText}</span>
              <span class="pool-plan">${esc(p.kind)}</span>
            </span>
            <span class="pool-machs" title="${esc(p.base_url || "")}">${esc(p.id)}</span>
          </div>
          <div class="pool-meters">
            ${meterRow(qLabel, qRight, qPct, !p.enabled && !hasQuota)}
            ${extraRow}
          </div>
          ${renderModelPills(tierPills, 3)}
        </div>
        <div class="pool-foot">
          <span>最近成功</span>
          <span>${ago ? esc(ago) + " · " : ""}${rt.requests || 0} 个请求${rt.failovers ? " · " + rt.failovers + " 切换" : ""}</span>
        </div>
      </div>`;
    };

    const allProvIDs = () => provs.map((p) => p.id).filter(Boolean);
    const renderTierChainHTML = (tierProvs, avail) => {
      const chips = tierProvs.map((pid, i) => `<span class="chip-btn" draggable="true" data-tc-item="${i}" style="display:inline-flex;align-items:center;gap:5px;cursor:grab;user-select:none">
        <span class="muted" style="font-size:11px;line-height:1">⠿</span>
        <span class="mono">${i + 1}. ${esc(pid)}</span>
        <button type="button" data-tc-del="${i}" title="移除" style="border:0;background:none;padding:0 2px;cursor:pointer;color:inherit">×</button>
      </span>`).join(`<span class="muted">→</span>`);
      const addable = avail.filter((id) => !tierProvs.includes(id));
      const addSel = addable.length
        ? `<select data-tc-add class="chip-btn" style="padding:2px 6px"><option value="">+ 添加</option>${addable.map((id) => `<option value="${esc(id)}">${esc(id)}</option>`).join("")}</select>`
        : "";
      return chips + addSel;
    };

    const initProvIDs = provs.map((p) => p.id);
    const tierRows = (cfg.tiers || []).map((t, idx) => `<div class="pool-tier-row" data-pool-tier="${idx}" data-tier-provs="${esc(JSON.stringify(t.providers || []))}">
      <input data-tf="id" class="mono" value="${esc(t.id)}">
      <input data-tf="label" value="${esc(t.label || "")}">
      <div data-tc-box style="display:flex;gap:6px;align-items:center;flex-wrap:wrap">${renderTierChainHTML(t.providers || [], initProvIDs)}</div>
      <input data-tf="description" value="${esc(t.description || "")}" placeholder="用途说明">
    </div>`).join("");

    body.innerHTML = `<div class="pool-wall">
      <div class="pool-toolbar">
        <div class="pool-summary">
          <button type="button" class="pool-stat ${poolStatusFilter === "ready" ? "on" : ""}" data-pst="ready"><span class="sdot" style="background:#34d399"></span>已启用 ${enabledCount}</button>
          <button type="button" class="pool-stat ${poolStatusFilter === "pending" ? "on" : ""}" data-pst="pending"><span class="sdot" style="background:#fbbf24"></span>待启用 ${pendingCount}</button>
          ${coolingCount ? `<button type="button" class="pool-stat ${poolStatusFilter === "cooling" ? "on" : ""}" data-pst="cooling"><span class="sdot" style="background:#f87171"></span>冷却 ${coolingCount}</button>` : ""}
        </div>
        <div class="pool-controls">
          <div class="seg">
            <button type="button" data-pfilter="all" class="${poolFilter === "all" ? "on" : ""}">全部</button>
            <button type="button" data-pfilter="sub" class="${poolFilter === "sub" ? "on" : ""}">原生订阅</button>
            <button type="button" data-pfilter="api" class="${poolFilter === "api" ? "on" : ""}">API 池</button>
          </div>
          <input type="search" id="pool-search" class="pool-search" value="${esc(poolQuery)}" placeholder="筛选模型 / 机器 / 通道…">
          <button type="button" class="btn small" id="pool-refresh">刷新</button>
          <button type="button" class="btn small" id="pool-add">+ 添加</button>
        </div>
      </div>
      <div id="pool-grid" class="pool-grid">
        ${subs.map(subCard).join("")}
        ${provs.map(provCard).join("")}
        <div id="pool-empty" class="pool-empty" hidden>当前筛选条件下没有匹配的账号或 Provider</div>
      </div>
      <div class="pool-tier-card">
        <div class="pool-tier-head">
          <span>模型档位故障切换链 · 统一反代入口 <code>${esc(baseV1)}</code>（拖拽芯片排序）</span>
          <span style="display:inline-flex;gap:10px;align-items:center;flex-wrap:wrap">
            <label class="check" style="margin:0;font-weight:500;font-size:12px" title="反代池全部限流或故障时，自动改用原生 CLI 订阅直连重跑"><input type="checkbox" id="pool-native-fb" ${cfg.native_fallback !== false ? "checked" : ""}> 派活订阅兜底</label>
            <button type="button" class="btn small" data-copy="${esc(codexSnippet)}">复制 Codex 接入</button>
            <button type="button" class="btn small" id="pool-reset-all">清除冷却</button>
            <button type="button" class="btn primary small" id="pool-save">保存模型池</button>
          </span>
        </div>
        <div id="pool-tiers">${tierRows}</div>
      </div>
    </div>`;

    const applyFilter = () => {
      const q = poolQuery.trim().toLowerCase();
      let shown = 0;
      body.querySelectorAll("#pool-grid > [data-card-cat]").forEach((el) => {
        const okCat = poolFilter === "all" || el.dataset.cardCat === poolFilter;
        const okSt = poolStatusFilter === "all" || el.dataset.cardSt === poolStatusFilter;
        const okQ = !q || (el.dataset.cardQ || "").includes(q);
        const vis = okCat && okSt && okQ;
        el.hidden = !vis;
        if (vis) shown++;
      });
      const empty = body.querySelector("#pool-empty");
      if (empty) empty.hidden = shown > 0;
    };
    applyFilter();

    const serializeProv = (p) => ({
      id: (p.id || "").trim(),
      name: (p.name || "").trim(),
      kind: p.kind || "openai",
      base_url: (p.base_url || "").trim(),
      console_url: (p.console_url || "").trim(),
      api_key: (p.api_key || "").trim(),
      proxy_url: (p.proxy_url || "").trim(),
      enabled: !!p.enabled,
      priority: +p.priority || 50,
      cooldown_sec: +p.cooldown_sec || 180,
      cached_models: Array.isArray(p.cached_models) ? p.cached_models : [],
      model_map: p.model_map || {},
    });

    const collectTiers = () => [...body.querySelectorAll("[data-pool-tier]")].map((el) => {
      let ps = [];
      try { ps = JSON.parse(el.dataset.tierProvs || "[]"); } catch {}
      return {
        id: el.querySelector("[data-tf=id]").value.trim(),
        label: el.querySelector("[data-tf=label]").value.trim(),
        description: el.querySelector("[data-tf=description]").value.trim(),
        providers: ps,
      };
    }).filter((t) => t.id);

    const collect = (nextProvs = provs, nextTiers = null) => ({
      providers: nextProvs.map(serializeProv).filter((p) => p.id),
      tiers: nextTiers || collectTiers(),
      native_fallback: body.querySelector("#pool-native-fb")?.checked ?? true,
    });

    const openProvModal = (idx, isNew = false) => {
      const orig = isNew
        ? { id: "custom-" + (provs.length + 1), name: "自定义 Provider", kind: "openai", base_url: "https://api.example.com/v1", enabled: false, priority: 50, model_map: {}, cached_models: [] }
        : provs[idx];
      if (!orig) return;
      let cachedModels = [...(orig.cached_models || [])];
      const mm = orig.model_map || {};
      const bg = document.createElement("div");
      bg.className = "pool-modal-bg";
      bg.innerHTML = `<div class="pool-modal" role="dialog" aria-modal="true">
        <div class="pool-modal-head">
          <h3>${isNew ? "添加 Provider" : esc(orig.name || orig.id)}</h3>
          <button type="button" class="pool-ico-btn" data-pm-close title="关闭">✕</button>
        </div>
        <div class="pool-modal-body">
          <div class="pool-toggle-row">
            <span>启用此通道</span>
            <label class="pool-switch"><input type="checkbox" data-pf="enabled" ${orig.enabled ? "checked" : ""}><span></span></label>
          </div>
          <div class="pool-sec">
            <div class="pool-sec-h"><span>基础信息</span></div>
            <div class="pool-form-grid">
              <label class="pool-field">名称<input data-pf="name" value="${esc(orig.name || "")}" placeholder="Provider 名称"></label>
              <label class="pool-field">通道 ID<input data-pf="id" class="mono" value="${esc(orig.id || "")}" placeholder="唯一标识"></label>
              <label class="pool-field">类型<select data-pf="kind">${[...new Set(["gateway", "openai", "proxy", "custom", orig.kind].filter(Boolean))].map((k) => `<option value="${esc(k)}" ${orig.kind === k ? "selected" : ""}>${esc(k)}</option>`).join("")}</select></label>
              <label class="pool-field">优先级（越小越优先）<input data-pf="priority" type="number" value="${orig.priority || 10}"></label>
            </div>
          </div>
          <div class="pool-sec">
            <div class="pool-sec-h"><span>接口与鉴权</span></div>
            <label class="pool-field">Base URL<input data-pf="base_url" class="mono" value="${esc(orig.base_url || "")}" placeholder="https://.../v1"></label>
            <label class="pool-field">API Key<input data-pf="api_key" class="mono" value="${esc(orig.api_key || "")}" placeholder="留空表示本地免鉴权或保持原值"></label>
            <label class="pool-field">HTTP 代理（可选）<input data-pf="proxy_url" class="mono" value="${esc(orig.proxy_url || "")}" placeholder="http://127.0.0.1:7890"></label>
          </div>
          <div class="pool-sec">
            <div class="pool-sec-h">
              <span id="pm-models-h">模型档位映射（${cachedModels.length} 模型）</span>
              <button type="button" class="btn small" id="pm-probe" style="height:26px;font-size:11.5px">探测模型</button>
            </div>
            <label class="pool-field">tier-fast（轻量快速）${modelSelectHTML("mm_fast", mm["tier-fast"] || "", cachedModels)}</label>
            <label class="pool-field">tier-standard（标准主力）${modelSelectHTML("mm_std", mm["tier-standard"] || "", cachedModels)}</label>
            <label class="pool-field">tier-strong（高配旗舰）${modelSelectHTML("mm_strong", mm["tier-strong"] || "", cachedModels)}</label>
          </div>
        </div>
        <div class="pool-modal-foot">
          ${!isNew ? `<button type="button" class="btn ghost small" id="pm-del" style="color:#ef4444">删除</button>` : ""}
          ${orig.console_url ? `<a class="btn small" href="${esc(orig.console_url)}" target="_blank" rel="noopener">控制台</a>` : ""}
          <span class="grow"></span>
          <button type="button" class="btn small" data-pm-close>取消</button>
          <button type="button" class="btn primary small" id="pm-save">保存</button>
        </div>
      </div>`;
      const close = () => { document.removeEventListener("keydown", onEsc); bg.remove(); };
      const onEsc = (ev) => { if (ev.key === "Escape") close(); };
      document.addEventListener("keydown", onEsc);
      document.body.appendChild(bg);

      const readModal = () => {
        const pick = (f) => { const v = bg.querySelector(`[data-pf=${f}]`).value.trim(); return v === "__custom" ? "" : v; };
        return {
          ...orig,
          id: bg.querySelector("[data-pf=id]").value.trim(),
          name: bg.querySelector("[data-pf=name]").value.trim(),
          kind: bg.querySelector("[data-pf=kind]").value,
          enabled: bg.querySelector("[data-pf=enabled]").checked,
          priority: +bg.querySelector("[data-pf=priority]").value || 50,
          base_url: bg.querySelector("[data-pf=base_url]").value.trim(),
          api_key: bg.querySelector("[data-pf=api_key]").value.trim(),
          proxy_url: bg.querySelector("[data-pf=proxy_url]").value.trim(),
          cached_models: cachedModels,
          model_map: {
            "tier-fast": pick("mm_fast"),
            "tier-standard": pick("mm_std"),
            "tier-strong": pick("mm_strong"),
          },
        };
      };

      bg.addEventListener("click", (ev) => { if (ev.target === bg || ev.target.closest("[data-pm-close]")) close(); });
      bg.addEventListener("change", async (ev) => {
        const mSel = ev.target.closest("select[data-pf^=mm_]");
        if (mSel && mSel.value === "__custom") {
          const v = await openInputModal({ title: "自定义上游模型 ID", label: "模型 ID", placeholder: "例如：gpt-5.4 / claude-sonnet-4-6" }, esc);
          if (v) mSel.add(new Option(v, v, true, true), 1);
          else mSel.value = "";
        }
      });
      bg.querySelector("#pm-probe")?.addEventListener("click", async (ev) => {
        const btn = ev.currentTarget;
        const p = readModal();
        btn.disabled = true; btn.textContent = "探测中…";
        try {
          const r = await api("/api/super/pool/probe", { body: { id: p.id, provider: serializeProv(p) } });
          if (r.ok) {
            if (Array.isArray(r.models) && r.models.length) {
              cachedModels = r.models;
              for (const f of ["mm_fast", "mm_std", "mm_strong"]) {
                const sel = bg.querySelector(`[data-pf=${f}]`);
                const cur = sel.value === "__custom" ? "" : sel.value;
                sel.outerHTML = modelSelectHTML(f, cur, cachedModels);
              }
              const h = bg.querySelector("#pm-models-h");
              if (h) h.textContent = `模型档位映射（${cachedModels.length} 模型）`;
            }
            toast(`${p.id} 连通正常（HTTP ${r.status}，${r.latency_ms}ms，拉取到 ${(r.models || []).length} 个模型）`);
          } else {
            toast(`${p.id} 未通过连通测试：${r.error || "HTTP " + r.status}`, "attn");
          }
        } catch (err) { toast(err.message, "attn"); }
        btn.disabled = false; btn.textContent = "探测模型";
      });
      bg.querySelector("#pm-save")?.addEventListener("click", async (ev) => {
        const btn = ev.currentTarget;
        const updated = readModal();
        if (!updated.id) { toast("通道 ID 不能为空", "attn"); return; }
        btn.disabled = true; btn.textContent = "保存中…";
        try {
          const nextProvs = isNew ? [...provs, updated] : provs.map((x, i) => (i === idx ? updated : x));
          await api("/api/super/pool", { body: collect(nextProvs) });
          close();
          toast(`${updated.name || updated.id} 已保存`);
          renderPool(body, api, esc, toast, false);
        } catch (err) { toast(err.message, "attn"); btn.disabled = false; btn.textContent = "保存"; }
      });
      bg.querySelector("#pm-del")?.addEventListener("click", async () => {
        if (!(await openConfirmModal({
          title: `删除通道「${orig.name || orig.id}」`,
          message: "删除后该通道将从模型池与所有档位故障切换链中移除。",
          confirmText: "确认删除",
          danger: true,
        }, esc))) return;
        try {
          const nextProvs = provs.filter((_, i) => i !== idx);
          const nextTiers = collectTiers().map((t) => ({ ...t, providers: t.providers.filter((id) => id !== orig.id) }));
          await api("/api/super/pool", { body: collect(nextProvs, nextTiers) });
          close();
          toast(`已删除 ${orig.id}`);
          renderPool(body, api, esc, toast, false);
        } catch (err) { toast(err.message, "attn"); }
      });
    };

    body.querySelector("#pool-refresh")?.addEventListener("click", () => renderPool(body, api, esc, toast, true));
    body.querySelectorAll("[data-pfilter]").forEach((b) => b.addEventListener("click", () => {
      poolFilter = b.dataset.pfilter;
      body.querySelectorAll("[data-pfilter]").forEach((x) => x.classList.toggle("on", x.dataset.pfilter === poolFilter));
      applyFilter();
    }));
    body.querySelectorAll("[data-pst]").forEach((b) => b.addEventListener("click", () => {
      const next = b.dataset.pst;
      poolStatusFilter = poolStatusFilter === next ? "all" : next;
      body.querySelectorAll("[data-pst]").forEach((x) => x.classList.toggle("on", x.dataset.pst === poolStatusFilter));
      applyFilter();
    }));
    body.querySelector("#pool-search")?.addEventListener("input", (e) => {
      poolQuery = e.target.value || "";
      applyFilter();
    });
    body.querySelector("#pool-add")?.addEventListener("click", () => openProvModal(-1, true));
    body.querySelector("#pool-save")?.addEventListener("click", async () => {
      try { await api("/api/super/pool", { body: collect() }); toast("模型池配置已保存"); renderPool(body, api, esc, toast, false); }
      catch (err) { toast(err.message, "attn"); }
    });
    body.querySelector("#pool-reset-all")?.addEventListener("click", async () => {
      try { await api("/api/super/pool/reset-cooldown", { body: {} }); toast("已清除全部冷却"); renderPool(body, api, esc, toast, false); }
      catch (err) { toast(err.message, "attn"); }
    });

    const wall = body.querySelector(".pool-wall");
    let dragCtx = null;
    wall.addEventListener("dragstart", (e) => {
      const chip = e.target.closest("[data-tc-item]");
      if (!chip) return;
      const row = chip.closest("[data-pool-tier]");
      dragCtx = { tier: row.dataset.poolTier, from: +chip.dataset.tcItem };
      chip.style.opacity = "0.45";
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", String(dragCtx.from));
    });
    wall.addEventListener("dragend", (e) => {
      const chip = e.target.closest("[data-tc-item]");
      if (chip) chip.style.opacity = "";
      wall.querySelectorAll("[data-tc-item]").forEach((el) => { el.style.outline = ""; });
      dragCtx = null;
    });
    wall.addEventListener("dragover", (e) => {
      const chip = e.target.closest("[data-tc-item]");
      if (!chip || !dragCtx) return;
      const row = chip.closest("[data-pool-tier]");
      if (row?.dataset.poolTier !== dragCtx.tier) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      wall.querySelectorAll("[data-tc-item]").forEach((el) => { el.style.outline = ""; });
      chip.style.outline = "2px solid var(--fg, #111)";
    });
    wall.addEventListener("drop", (e) => {
      const chip = e.target.closest("[data-tc-item]");
      if (!chip || !dragCtx) return;
      const row = chip.closest("[data-pool-tier]");
      if (row?.dataset.poolTier !== dragCtx.tier) return;
      e.preventDefault();
      const from = dragCtx.from, to = +chip.dataset.tcItem;
      if (from === to) return;
      const ps = JSON.parse(row.dataset.tierProvs || "[]");
      const [moved] = ps.splice(from, 1);
      ps.splice(to, 0, moved);
      row.dataset.tierProvs = JSON.stringify(ps);
      row.querySelector("[data-tc-box]").innerHTML = renderTierChainHTML(ps, allProvIDs());
    });
    wall.addEventListener("change", (e) => {
      const addSel = e.target.closest("[data-tc-add]");
      if (addSel && addSel.value) {
        const row = addSel.closest("[data-pool-tier]");
        const ps = JSON.parse(row.dataset.tierProvs || "[]");
        if (!ps.includes(addSel.value)) ps.push(addSel.value);
        row.dataset.tierProvs = JSON.stringify(ps);
        row.querySelector("[data-tc-box]").innerHTML = renderTierChainHTML(ps, allProvIDs());
      }
    });
    wall.addEventListener("click", async (e) => {
      const editBtn = e.target.closest("[data-pool-edit]");
      if (editBtn) { openProvModal(+editBtn.dataset.poolEdit, false); return; }
      const tdel = e.target.closest("[data-tc-del]");
      if (tdel) {
        const row = tdel.closest("[data-pool-tier]");
        const ps = JSON.parse(row.dataset.tierProvs || "[]");
        ps.splice(+tdel.dataset.tcDel, 1);
        row.dataset.tierProvs = JSON.stringify(ps);
        row.querySelector("[data-tc-box]").innerHTML = renderTierChainHTML(ps, allProvIDs());
        return;
      }
      const ve = e.target.closest("[data-pool-verify-enable]");
      if (ve) {
        const idx = +ve.dataset.poolVerifyEnable;
        const orig = provs[idx];
        if (!orig) return;
        ve.disabled = true; ve.textContent = "测试中…";
        try {
          const r = await api("/api/super/pool/probe", { body: { id: orig.id, provider: serializeProv(orig) } });
          if (r.ok) {
            const updated = { ...orig, enabled: true, cached_models: (Array.isArray(r.models) && r.models.length) ? r.models : (orig.cached_models || []) };
            const nextProvs = provs.map((x, i) => (i === idx ? updated : x));
            await api("/api/super/pool", { body: collect(nextProvs) });
            toast(`${orig.id} 实测连通（HTTP ${r.status}，${r.latency_ms}ms，${(r.models || []).length} 个模型）并已启用`);
            renderPool(body, api, esc, toast, false);
            return;
          }
          toast(`${orig.id} 未通过连通测试：${r.error || "HTTP " + r.status}（未启用）`, "attn");
        } catch (err) { toast(err.message, "attn"); }
        if (ve.isConnected) { ve.disabled = false; ve.textContent = "启用"; }
        return;
      }
      const rs = e.target.closest("[data-pool-reset]");
      if (rs) {
        try { await api("/api/super/pool/reset-cooldown", { body: { id: rs.dataset.poolReset } }); toast("已解除冷却"); renderPool(body, api, esc, toast, false); }
        catch (err) { toast(err.message, "attn"); }
      }
    });
  }

  let inboxView = "need";
  let inboxCat = "all";
  let inboxQuery = "";
  const inboxOpen = new Set();

  function renderRichSnippet(raw, esc) {
    const src = String(raw || "");
    const re = /https?:\/\/[^\s)）>]+/g;
    let out = "", last = 0, m;
    while ((m = re.exec(src)) !== null) {
      out += esc(src.slice(last, m.index));
      const u = m[0].replace(/[.,;，。；]+$/, "");
      const trail = m[0].slice(u.length);
      let label = "链接 ↗";
      if (/applink\.feishu\.cn\/client\/chat/i.test(u)) label = "飞书消息 ↗";
      else if (/larkoffice\.com\/docx|feishu\.cn\/docx/i.test(u)) label = "飞书文档 ↗";
      else if (/larkoffice\.com\/wiki|feishu\.cn\/wiki/i.test(u)) label = "飞书知识库 ↗";
      else if (/larkoffice\.com|feishu\.cn/i.test(u)) label = "飞书链接 ↗";
      else {
        try { const h = new URL(u).hostname.replace(/^www\./, ""); label = `${h} ↗`; } catch {}
      }
      out += `<a class="pi-link" href="${esc(u)}" target="_blank" rel="noopener">${esc(label)}</a>${esc(trail)}`;
      last = m.index + m[0].length;
    }
    return out + esc(src.slice(last));
  }

  function classifyInbox(t, acked, activeWorkers, needReasons) {
    const txt = String(t.text || "");
    const low = txt.toLowerCase();
    const from = String(t.from || "");
    const fromLow = from.toLowerCase();
    const isFeishuTask = fromLow.includes("feishu") || txt.includes("【工作日飞书只读 Todo") || low.includes("feishu-todo");
    let cat = "agent", catLabel = "助手 / Agent", av = "助", avCls = "pi-av-agent";
    if (t.source === "chat") { cat = "chat"; catLabel = "对话"; av = "你"; avCls = "pi-av-chat"; }
    else if (isFeishuTask) { cat = "feishu"; catLabel = "飞书 / Todo"; av = "飞"; avCls = "pi-av-feishu"; }
    else if (t.source === "timer") { cat = "timer"; catLabel = "定时"; av = "定"; avCls = "pi-av-timer"; }
    else if (t.source === "system" || t.source === "router") { cat = "sys"; catLabel = "系统"; av = "系"; avCls = "pi-av-sys"; }
    else if (t.source && t.source !== "worker") { cat = "hook"; catLabel = t.source; av = "接"; avCls = "pi-av-sys"; }

    const isAck = !!acked[t.id];
    const activeW = activeWorkers && from ? activeWorkers[from] : null;
    const isBlocked = /is blocked|block(ed)?:/i.test(txt);
    const isFailed = /failed to start|marked failed|worker .* failed:/i.test(txt);

    let needReason = "";
    if (!isAck) {
      if (needReasons) {
        needReason = needReasons[t.id] || "";
      } else if (t.action === "candidate") needReason = "外部事件，等你决定";
      else if (isBlocked && activeW && activeW.status === "blocked") needReason = "当前阻塞中，需去终端确认";
      else if (isFailed && activeW) needReason = "运行异常，需查看";
    }
    const needsYou = !!needReason;
    let bodyText = txt.trim();
    const repM = bodyText.match(/^report from ([^:\r\n]+):?\s*/i);
    let title = "";
    if (repM) {
      bodyText = bodyText.slice(repM[0].length).trim();
      const firstBodyLine = (bodyText.split(/\r?\n/).map((l) => l.trim()).find(Boolean) || "");
      title = firstBodyLine ? `${repM[1].trim()} · ${firstBodyLine}` : `${repM[1].trim()} 汇报`;
    } else {
      const lines = bodyText.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
      title = (lines[0] || t.id).replace(/^worker\s+/i, "");
    }
    if (title.length > 58) title = title.slice(0, 58) + "…";
    const fullText = bodyText || txt.trim();
    const bodyLines = fullText.split(/\r?\n/).filter((l) => l.trim());
    const canExpand = fullText.length > 110 || bodyLines.length > 2;
    const paneID = activeW?.pane_id || "";
    const firstURL = (txt.match(/https?:\/\/[^\s)）>]+/) || [""])[0];
    return { cat, catLabel, av, avCls, needsYou, needReason, isAck, title, fullText, canExpand, paneID, firstURL };
  }

  function syncInboxBadges(n) {
    const tip = n ? `告警与日志中有 ${n} 条需要你处理` : "";
    for (const sel of ["#nav-queue", "#inbox-badge"]) {
      const el = document.querySelector(sel);
      if (el) { el.hidden = !n; el.textContent = n || ""; el.title = tip; }
    }
  }

  function openTodoModal(defaults, api, esc, toast, opts = {}) {
    const d = defaults || {};
    const bg = document.createElement("div");
    bg.className = "pool-modal-bg";
    const src = d.source || "manual";
    const pri = d.priority || "medium";
    const ackId = d.ackId || "";
    bg.innerHTML = `<div class="pool-modal" role="dialog" aria-modal="true">
      <div class="pool-modal-head">
        <h3>${d.modalTitle || "新建待办"}</h3>
        <button type="button" class="pool-ico-btn" data-tm-close title="关闭">✕</button>
      </div>
      <div class="pool-modal-body">
        <label class="pool-field">标题 *<input data-tf="title" value="${esc(d.title || "")}" placeholder="例如：确认本周模型部署与排期"></label>
        <div class="pool-form-grid">
          <label class="pool-field">来源<select data-tf="source">
            <option value="manual" ${src === "manual" ? "selected" : ""}>手动待办</option>
            <option value="feishu" ${src === "feishu" ? "selected" : ""}>飞书待办</option>
            <option value="agent" ${src === "agent" ? "selected" : ""}>助手整理</option>
          </select></label>
          <label class="pool-field">优先级<select data-tf="priority">
            <option value="high" ${pri === "high" ? "selected" : ""}>高优先</option>
            <option value="medium" ${pri === "medium" ? "selected" : ""}>普通</option>
            <option value="low" ${pri === "low" ? "selected" : ""}>低优先</option>
          </select></label>
        </div>
        <div class="pool-form-grid">
          <label class="pool-field">截止时间（可选）<input data-tf="due" value="${esc(d.due || "")}" placeholder="今天 18:00 / 周五"></label>
          <label class="pool-field">关联链接（可选）<input data-tf="link" class="mono" value="${esc(d.link || "")}" placeholder="https://..."></label>
        </div>
        <label class="pool-field">补充说明（可选）<textarea data-tf="detail" placeholder="背景上下文、验收标准或相关线索…">${esc(d.detail || "")}</textarea></label>
        ${ackId ? `<label class="pi-check-row"><input type="checkbox" data-tf="ack" checked> <span>创建后同时将原告警条目「标为已看」</span></label>` : ""}
      </div>
      <div class="pool-modal-foot">
        <span class="grow"></span>
        <button type="button" class="btn small" data-tm-close>取消</button>
        ${opts.onDispatch ? `<button type="button" class="btn small" id="tm-save-go">保存并派给 Swag</button>` : ""}
        <button type="button" class="btn primary small" id="tm-save">保存待办</button>
      </div>
    </div>`;
    const close = () => { document.removeEventListener("keydown", onEsc); bg.remove(); };
    const onEsc = (ev) => { if (ev.key === "Escape") close(); };
    document.addEventListener("keydown", onEsc);
    document.body.appendChild(bg);
    const titleInput = bg.querySelector("[data-tf=title]");
    if (titleInput) { titleInput.focus(); titleInput.select(); }

    const submit = async (andDispatch) => {
      const title = bg.querySelector("[data-tf=title]").value.trim();
      if (!title) { toast("待办标题不能为空", "attn"); titleInput?.focus(); return; }
      const payload = {
        title,
        source: bg.querySelector("[data-tf=source]").value,
        priority: bg.querySelector("[data-tf=priority]").value,
        due: bg.querySelector("[data-tf=due]").value.trim(),
        link: bg.querySelector("[data-tf=link]").value.trim(),
        detail: bg.querySelector("[data-tf=detail]").value.trim(),
        status: andDispatch ? "dispatched" : "open",
      };
      const doAck = ackId && bg.querySelector("[data-tf=ack]")?.checked;
      const saveBtn = bg.querySelector("#tm-save");
      const goBtn = bg.querySelector("#tm-save-go");
      if (saveBtn) saveBtn.disabled = true;
      if (goBtn) goBtn.disabled = true;
      try {
        const saved = await api("/api/super/todos", { body: payload });
        const todoId = saved?.todo?.id || "";
        if (doAck) await api("/api/super/inbox/ack", { body: { ids: [ackId] } });
        close();
        toast(andDispatch ? "已创建待办并派给 Swag" : "已添加到「待办」");
        if (opts.onSaved) opts.onSaved({ ackedId: doAck ? ackId : "" });
        if (andDispatch && opts.onDispatch) {
          const idTag = todoId ? `[todo:${todoId}] ` : "";
          opts.onDispatch(`帮我处理这条待办：\n${idTag}「${payload.title}」${payload.detail ? "\n补充信息：" + payload.detail : ""}${payload.link ? "\n链接：" + payload.link : ""}`);
        }
      } catch (err) {
        toast(err.message, "attn");
        if (saveBtn) saveBtn.disabled = false;
        if (goBtn) goBtn.disabled = false;
      }
    };

    bg.addEventListener("click", (ev) => { if (ev.target === bg || ev.target.closest("[data-tm-close]")) close(); });
    bg.querySelector("#tm-save")?.addEventListener("click", () => submit(false));
    bg.querySelector("#tm-save-go")?.addEventListener("click", () => submit(true));
    titleInput?.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" && !ev.isComposing) { ev.preventDefault(); submit(false); }
    });
  }

  function fmtClock(iso) {
    if (!iso || String(iso).startsWith("0001-01-01")) return "";
    const d = new Date(iso);
    if (!Number.isFinite(d.getTime())) return "";
    const p2 = (v) => String(v).padStart(2, "0");
    const now = new Date();
    if (d.toDateString() === now.toDateString()) return `${p2(d.getHours())}:${p2(d.getMinutes())}`;
    return `${p2(d.getMonth() + 1)}/${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`;
  }

  function renderInbox(body, triggers, acked, activeWorkers, api, esc, toast, onRefresh, needReasons, onDispatch, threads) {
    const activeSearch = document.activeElement?.id === "pi-search";
    const items = (triggers || []).map((t) => ({ t, m: classifyInbox(t, acked || {}, activeWorkers || {}, needReasons || null) }));
    const needItems = items.filter((x) => x.m.needsYou);
    syncInboxBadges(needItems.length);
    const threadList = Array.isArray(threads) ? threads : [];

    const matchTab = (x) => (inboxView === "need" ? x.m.needsYou : inboxView === "done" ? !x.m.needsYou : true);
    const q = inboxQuery.trim().toLowerCase();
    const filtered = inboxView === "threads" ? [] : items.filter((x) => {
      if (!matchTab(x)) return false;
      if (inboxCat !== "all" && x.m.cat !== inboxCat) return false;
      if (q && !(x.m.title + " " + x.t.text + " " + (x.t.from || "") + " " + x.m.catLabel).toLowerCase().includes(q)) return false;
      return true;
    });
    const filteredThreads = inboxView === "threads"
      ? threadList.filter((th) => !q || `${th.id || ""} ${(th.subscribers || []).join(" ")}`.toLowerCase().includes(q))
      : [];

    const groups = new Map();
    for (const x of filtered) {
      const k = x.m.catLabel;
      if (!groups.has(k)) groups.set(k, []);
      groups.get(k).push(x);
    }

    const visibleNeed = filtered.filter((x) => x.m.needsYou);
    const catNameMap = { all: "全部", feishu: "飞书 / Todo", agent: "助手 / Agent", timer: "定时", chat: "对话", sys: "系统", hook: "Webhook" };
    const clearLabel = (inboxCat !== "all" || q)
      ? `✓ 标为已看 · 当前「${catNameMap[inboxCat] || inboxCat}」(${visibleNeed.length})`
      : `✓ 全部标为已看 (${visibleNeed.length})`;

    const cardHTML = ({ t, m }) => {
      const isOpen = inboxOpen.has(t.id);
      return `<div class="pi-card ${isOpen ? "is-open" : ""}" data-pi-id="${esc(t.id)}">
        <div class="pi-avatar ${m.avCls}">${esc(m.av)}</div>
        <div class="pi-body">
          <div class="pi-top">
            <div class="pi-title" title="${esc(m.title)}">${esc(m.title)}<span class="pi-tag">${esc(m.catLabel)}</span></div>
            <span class="pi-time">${esc(fmtClock(t.at))}</span>
          </div>
          <div class="pi-snippet">${renderRichSnippet(m.fullText, esc)}</div>
          <div class="pi-foot">
            <span class="pi-status"><span class="pi-dot ${m.needsYou ? "pi-dot-need" : "pi-dot-done"}"></span>${esc(m.needsYou ? m.needReason : (t.status === "queued" ? "排队等 Swag 处理" : (m.isAck ? "已标为已看" : "已处理")))}</span>
            <span class="mono">#${esc(t.id)}</span>
            <span class="grow"></span>
            ${m.canExpand ? `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-pi-toggle="${esc(t.id)}">${isOpen ? "收起" : "展开全文"}</button>` : ""}
            <button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-pi-todo="${esc(t.id)}">+ 转为待办</button>
            ${m.needsYou ? `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-pi-ack="${esc(t.id)}">标为已看</button>` : ""}
            ${m.paneID ? `<a class="btn small" style="height:24px;font-size:11.5px;text-decoration:none" href="#/p/${encodeURIComponent(m.paneID)}">查看终端</a>` : `<a class="btn ghost small" style="height:24px;font-size:11.5px;text-decoration:none" href="#/swag">主对话</a>`}
          </div>
        </div>
      </div>`;
    };

    const groupsHTML = [...groups.entries()].map(([label, list]) => `<details class="pi-group" open>
      <summary><span>▾</span><span>${esc(label)}</span><span class="muted">${list.length}</span></summary>
      <div class="pi-list">${list.map(cardHTML).join("")}</div>
    </details>`).join("");

    const cats = [
      ["all", "全部"],
      ["feishu", "飞书 / Todo"],
      ["agent", "助手 / Agent"],
      ["timer", "定时"],
      ["chat", "对话"],
      ["sys", "系统"],
      ["hook", "Webhook"],
    ];

    const threadsHTML = inboxView === "threads"
      ? `<p class="panel-intro" style="margin:4px 0 12px">同一个对话、Issue/PR 或群聊话题里的连续事件构成一条线程。Swag 派出的 agent 订阅线程后，后续事件直达它，不再经过 Swag。<code>chat:main</code> 始终由 Swag 自己处理。</p>
        <div class="pi-list">${filteredThreads.length ? filteredThreads.map((th) => `<div class="pi-card">
          <div class="pi-avatar pi-av-sys">线</div>
          <div class="pi-body">
            <div class="pi-top">
              <div class="pi-title mono">${esc(th.id)} <span class="muted">→ ${esc((th.subscribers || []).length ? th.subscribers.join(", ") : "Swag")}</span></div>
              <span class="pi-time">${esc(fmtClock(th.last_at))}</span>
            </div>
            <div class="pi-foot"><span class="pi-status"><span class="pi-dot pi-dot-done"></span>${(th.triggers || []).length} 条事件</span></div>
          </div>
        </div>`).join("") : `<div class="pool-empty">还没有活跃线程</div>`}</div>`
      : "";

    const prevPos = activeSearch ? document.activeElement.selectionStart : null;
    body.innerHTML = `<div class="pi-wrap">
      <div class="pi-bar">
        <div class="pi-tabs">
          <button type="button" class="pi-tab ${inboxView === "need" ? "on" : ""}" data-piv="need">需要你${needItems.length ? `<span class="cnt">${needItems.length}</span>` : ""}</button>
          <button type="button" class="pi-tab ${inboxView === "done" ? "on" : ""}" data-piv="done">已处理</button>
          <button type="button" class="pi-tab ${inboxView === "all" ? "on" : ""}" data-piv="all">全部 <span class="muted">${items.length}</span></button>
          <button type="button" class="pi-tab ${inboxView === "threads" ? "on" : ""}" data-piv="threads">线程 <span class="muted">${threadList.length}</span></button>
        </div>
        <span class="grow"></span>
        ${inboxView !== "threads" && visibleNeed.length ? `<button type="button" class="btn small" id="pi-clear-all">${esc(clearLabel)}</button>` : ""}
      </div>
      <input type="search" id="pi-search" class="pi-search" value="${esc(inboxQuery)}" placeholder="${inboxView === "threads" ? "搜索线程 ID、订阅者…" : "搜索标题、来源、内容…"}">
      ${inboxView === "threads" ? "" : `<div class="pi-chips">${cats.map(([k, l]) => `<button type="button" class="pi-chip ${inboxCat === k ? "on" : ""}" data-pic="${k}">${l}</button>`).join("")}</div>`}
      ${inboxView === "threads" ? threadsHTML : (groupsHTML || `<div class="pool-empty">${inboxView === "need" ? "当前筛选下没有需要你处理的告警 🎉" : "没有匹配的事件日志"}</div>`)}
    </div>`;

    if (activeSearch) {
      const s = body.querySelector("#pi-search");
      if (s) { s.focus(); if (prevPos != null) s.setSelectionRange(prevPos, prevPos); }
    }

    const byId = new Map(items.map((x) => [x.t.id, x]));
    const rerender = () => renderInbox(body, triggers, acked, activeWorkers, api, esc, toast, onRefresh, needReasons, onDispatch, threadList);
    const wrap = body.querySelector(".pi-wrap");
    wrap.addEventListener("click", async (e) => {
      const vt = e.target.closest("[data-piv]");
      if (vt) { inboxView = vt.dataset.piv; rerender(); return; }
      const ct = e.target.closest("[data-pic]");
      if (ct) { inboxCat = ct.dataset.pic; rerender(); return; }
      const tog = e.target.closest("[data-pi-toggle]");
      if (tog) {
        const id = tog.dataset.piToggle;
        if (inboxOpen.has(id)) inboxOpen.delete(id); else inboxOpen.add(id);
        rerender();
        return;
      }
      const toTodo = e.target.closest("[data-pi-todo]");
      if (toTodo) {
        const x = byId.get(toTodo.dataset.piTodo);
        if (!x) return;
        const src = x.m.cat === "feishu" ? "feishu" : x.m.cat === "agent" ? "agent" : "manual";
        openTodoModal({
          modalTitle: "从告警日志转为待办",
          title: x.m.title.replace(/汇报：$/, "").trim(),
          source: src,
          priority: "medium",
          link: x.m.firstURL || "",
          detail: x.m.fullText.slice(0, 600),
          ackId: x.m.needsYou ? x.t.id : "",
        }, api, esc, toast, {
          onDispatch,
          onSaved: ({ ackedId }) => {
            if (ackedId) acked[ackedId] = true;
            rerender();
          },
        });
        return;
      }
      const ackOne = e.target.closest("[data-pi-ack]");
      if (ackOne) {
        const id = ackOne.dataset.piAck;
        try {
          await api("/api/super/inbox/ack", { body: { ids: [id] } });
          acked[id] = true;
          rerender();
        } catch (err) { toast(err.message, "attn"); }
        return;
      }
      if (e.target.closest("#pi-clear-all")) {
        const ids = visibleNeed.map((x) => x.t.id);
        if (!ids.length) return;
        try {
          await api("/api/super/inbox/ack", { body: { ids } });
          ids.forEach((id) => { acked[id] = true; });
          toast(`已将当前筛选的 ${ids.length} 条标为已看`);
          rerender();
        } catch (err) { toast(err.message, "attn"); }
      }
    });
    wrap.querySelector("#pi-search")?.addEventListener("input", (e) => {
      inboxQuery = e.target.value || "";
      rerender();
    });
  }

  let todoFilter = "open";
  let todoSrc = "all";
  let docFilter = "all";
  let docWeek = "latest";
  let todoQuery = "";
  let todoBatchMode = false;
  const todoSelected = new Set();

  function isoWeekKey(dt = new Date()) {
    const d = new Date(Date.UTC(dt.getFullYear(), dt.getMonth(), dt.getDate()));
    const day = d.getUTCDay() || 7;
    d.setUTCDate(d.getUTCDate() + 4 - day);
    const yStart = new Date(Date.UTC(d.getUTCFullYear(), 0, 1));
    const wk = Math.ceil((((d - yStart) / 86400000) + 1) / 7);
    return `${d.getUTCFullYear()}-W${String(wk).padStart(2, "0")}`;
  }

  function openDispatchModal(items, esc, toast, onDispatch) {
    const list = (items || []).filter(Boolean);
    if (!list.length || !onDispatch) return;
    const bg = document.createElement("div");
    bg.className = "pool-modal-bg";
    const quickChips = [
      "先只读核查上下文与风险点，给我结论，不要执行写操作或代批",
      "帮我梳理行动方案与下一步建议",
      "帮我起草回复内容给我确认",
      "按常规方式直接推进并在关键节点向我汇报",
    ];
    bg.innerHTML = `<div class="pool-modal" role="dialog" aria-modal="true">
      <div class="pool-modal-head">
        <h3>${list.length > 1 ? `批量派给 Swag（${list.length} 条待办）` : "派给 Swag 处理"}</h3>
        <button type="button" class="pool-ico-btn" data-dm-close title="关闭">✕</button>
      </div>
      <div class="pool-modal-body">
        <div class="td-dispatch-list">
          ${list.map((t, i) => `<div class="td-dispatch-item"><b>${list.length > 1 ? `${i + 1}. ` : ""}${esc(t.title)}</b>${t.detail ? `<span>${esc(t.detail.slice(0, 140))}</span>` : ""}${t.link ? `<span class="mono" style="font-size:11px;opacity:.8">${esc(t.link)}</span>` : ""}</div>`).join("")}
        </div>
        <div class="td-quick-chips">
          ${quickChips.map((q) => `<button type="button" class="td-quick-chip" data-dm-chip="${esc(q)}">${esc(q.slice(0, 16))}${q.length > 16 ? "…" : ""}</button>`).join("")}
        </div>
        <label class="pool-field">给 Swag 的要求 / Prompt（留空则按默认要求处理，Cmd/Ctrl+Enter 发送）
          <textarea data-dm-prompt placeholder="例如：只读检查这个工单的 SQL 风险并总结给我，千万不要自动审批…"></textarea>
        </label>
      </div>
      <div class="pool-modal-foot">
        <span class="grow"></span>
        <button type="button" class="btn small" data-dm-close>取消</button>
        <button type="button" class="btn primary small" id="dm-send">发送给 Swag</button>
      </div>
    </div>`;
    const close = () => { document.removeEventListener("keydown", onEsc); bg.remove(); };
    const onEsc = (ev) => { if (ev.key === "Escape") close(); };
    document.addEventListener("keydown", onEsc);
    document.body.appendChild(bg);
    const ta = bg.querySelector("[data-dm-prompt]");
    ta?.focus();

    const doSend = () => {
      const promptTxt = (ta?.value || "").trim();
      const bodyBlocks = list.map((t, i) => {
        const prefix = list.length > 1 ? `${i + 1}. ` : "";
        const idTag = t.id ? `[todo:${t.id}] ` : "";
        return `${prefix}${idTag}「${t.title}」${t.detail ? "\n   补充：" + t.detail : ""}${t.link ? "\n   链接：" + t.link : ""}`;
      }).join("\n");
      const msg = promptTxt
        ? `${promptTxt}\n\n关联待办：\n${bodyBlocks}`
        : `帮我处理以下待办：\n${bodyBlocks}`;
      close();
      onDispatch(msg, list);
    };

    bg.addEventListener("click", (ev) => {
      if (ev.target === bg || ev.target.closest("[data-dm-close]")) { close(); return; }
      const chip = ev.target.closest("[data-dm-chip]");
      if (chip && ta) { ta.value = chip.dataset.dmChip; ta.focus(); }
    });
    bg.querySelector("#dm-send")?.addEventListener("click", doSend);
    ta?.addEventListener("keydown", (ev) => {
      if ((ev.metaKey || ev.ctrlKey) && ev.key === "Enter") { ev.preventDefault(); doSend(); }
    });
  }

  async function recallMsg(id, api, toast, onRemoved, inputEl) {
    if (!id) return;
    const escTxt = (s) => String(s || "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;" }[c]));
    if (!(await openConfirmModal({
      title: "撤回这条消息",
      message: "若 Swag 尚未读取将直接拦截；若已开始处理会自动通知其忽略，原文字会回填到输入框方便重新编辑。",
      confirmText: "确认撤回",
      danger: true,
    }, escTxt))) return;
    try {
      const r = await api("/api/super/recall", { body: { id } });
      if (onRemoved) onRemoved(id);
      if (inputEl && !inputEl.value.trim() && r.text) {
        inputEl.value = r.text;
        inputEl.dispatchEvent(new Event("input", { bubbles: true }));
      }
      toast(r.cancelled_queued ? "已撤回（Swag 尚未收到，已拦截）" : "已撤回消息（已通知 Swag 忽略）");
    } catch (err) {
      toast(err.message, "attn");
    }
  }

  async function renderTodos(body, api, esc, toast, onDispatch) {
    const gen = body._autoGen;
    if (!body.querySelector(".td-wrap")) {
      body.innerHTML = `<p class="panel-intro">正在加载待办、进行中任务与待审批事项…</p>`;
    }
    let data;
    try {
      data = await api("/api/super/todos");
    } catch (err) {
      if (body._autoGen !== gen) return;
      body.innerHTML = `<p class="panel-intro">读取待办失败：${esc(err.message)}</p>`;
      return;
    }
    if (body._autoGen !== gen) return;
    let todos = data.todos || [];
    const liveTasks = data.live_tasks || [];
    const pending = data.pending || [];
    let deliverables = data.deliverables || [];

    const syncTodoBadge = () => {
      const openTodos = todos.filter((t) => !t.status || t.status === "open").length;
      const total = openTodos + liveTasks.length + pending.length;
      const badge = document.querySelector("#todo-badge");
      if (badge) {
        badge.hidden = !total;
        badge.textContent = total || "";
      }
      return total;
    };
    syncTodoBadge();

    const silentReload = async () => {
      try {
        const fresh = await api("/api/super/todos");
        todos = fresh.todos || [];
        deliverables = fresh.deliverables || [];
        syncTodoBadge();
        paint();
      } catch {}
    };

    const paint = () => {
      const prevScroll = body.scrollTop;
      const activeSearch = document.activeElement?.id === "td-search";
      const prevPos = activeSearch ? document.activeElement.selectionStart : null;

      const existingIds = new Set(todos.map((t) => t.id));
      for (const id of [...todoSelected]) if (!existingIds.has(id)) todoSelected.delete(id);

      const q = todoQuery.trim().toLowerCase();
      const matchQ = (s) => !q || String(s || "").toLowerCase().includes(q);
      const showLiveBand = todoFilter === "open" || todoFilter === "all";
      const showPend = showLiveBand && (todoSrc === "all" || todoSrc === "pending")
        ? pending.filter((p) => matchQ((p.summary || "") + " " + (p.text || "") + " " + (p.options || []).join(" ")))
        : [];
      const showTasks = showLiveBand && (todoSrc === "all" || todoSrc === "task")
        ? liveTasks.filter((t) => matchQ(t.title + " " + t.summary + " " + t.name))
        : [];
      const showTodos = todoFilter === "docs" ? [] : todos.filter((t) => {
        const st = t.status || "open";
        if (todoFilter === "open" && st !== "open") return false;
        if (todoFilter === "dispatched" && st !== "dispatched") return false;
        if (todoFilter === "done" && st !== "done") return false;
        if (todoSrc !== "all" && t.source !== todoSrc) return false;
        return matchQ(t.title + " " + (t.detail || "") + " " + (t.source || ""));
      });
      const curWeekKey = isoWeekKey(new Date());
      const prevWeekKey = isoWeekKey(new Date(Date.now() - 7 * 86400000));
      const weekMap = new Map();
      for (const d of deliverables) {
        const wk = d.week || "未分周";
        if (!weekMap.has(wk)) weekMap.set(wk, { key: wk, label: d.week_label || wk, count: 0 });
        weekMap.get(wk).count++;
      }
      const weekList = [...weekMap.values()].sort((a, b) => (a.key < b.key ? 1 : a.key > b.key ? -1 : 0));
      const prettyWeek = (w) => {
        if (!w || w.key === "未分周") return "未分周";
        if (w.key === curWeekKey) return `本周 · ${w.label}`;
        if (w.key === prevWeekKey) return `上周 · ${w.label}`;
        return w.label;
      };
      const activeWeek = docWeek === "all"
        ? "all"
        : (docWeek === "latest" || !weekMap.has(docWeek))
          ? (weekList[0]?.key || "all")
          : docWeek;
      const weekScoped = activeWeek === "all"
        ? deliverables
        : deliverables.filter((d) => (d.week || "未分周") === activeWeek);
      const docsWeeklyDoc = deliverables.filter((d) => !!d.url && d.doc_type === "周总复盘").length;
      const scopedWeeklyDoc = weekScoped.filter((d) => !!d.url && d.doc_type === "周总复盘").length;
      const docsSpecialDoc = weekScoped.filter((d) => !!d.url && d.doc_type !== "周总复盘").length;
      const docsWeekly = weekScoped.filter((d) => !d.url && d.scope === "weekly").length;
      const docsMissing = weekScoped.filter((d) => !d.url && d.scope !== "weekly").length;
      const showDocs = todoFilter === "docs"
        ? weekScoped.filter((d) => {
          if (docFilter === "weekly_doc" && !(d.url && d.doc_type === "周总复盘")) return false;
          if (docFilter === "special" && !(d.url && d.doc_type !== "周总复盘")) return false;
          if (docFilter === "missing" && (d.url || d.scope === "weekly")) return false;
          if (docFilter === "weekly" && (d.url || d.scope !== "weekly")) return false;
          const relHay = (d.related_tasks || []).join(" ");
          return matchQ(d.title + " " + (d.task_title || "") + " " + relHay + " " + (d.summary || "") + " " + (d.source || "") + " " + (d.doc_type || "") + " " + (d.week || ""));
        })
        : [];

      const pendHTML = showPend.map((p) => {
        const bodyTxt = (p.text || p.summary || "").trim();
        const headTxt = (p.summary || bodyTxt.split("\n")[0] || "待你确认").slice(0, 64);
        const isOpen = inboxOpen.has("pend:" + p.id);
        const optsHTML = (p.options || []).length
          ? `<div class="pi-opts">${p.options.map((o, idx) => `<button type="button" class="btn ${idx === 0 ? "primary" : ""} small" style="height:26px;font-size:12px" data-td-reply-re="${esc(p.id)}" data-td-reply-opt="${esc(o)}">${esc(o)}</button>`).join("")}</div>`
          : "";
        return `<div class="pi-card ${isOpen ? "is-open" : ""}">
          <div class="pi-avatar pi-av-agent">审</div>
          <div class="pi-body">
            <div class="pi-top">
              <div class="pi-title" title="${esc(headTxt)}">${esc(headTxt)}<span class="pi-tag">待审批</span></div>
              <span class="pi-time">${esc(fmtClock(p.at))}</span>
            </div>
            <div class="pi-snippet">${esc(bodyTxt)}</div>
            ${optsHTML}
            <div class="pi-foot">
              <span class="pi-status"><span class="pi-dot pi-dot-need"></span>需要你确认</span>
              <span class="grow"></span>
              ${bodyTxt.length > 90 ? `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-expand="pend:${esc(p.id)}">${isOpen ? "收起" : "展开全文"}</button>` : ""}
              <a class="btn ghost small" style="height:24px;font-size:11.5px;text-decoration:none" href="#/swag">去主对话</a>
            </div>
          </div>
        </div>`;
      }).join("");

      const taskHTML = showTasks.map((t) => {
        const key = "task:" + t.name;
        const isOpen = inboxOpen.has(key);
        const bodyTxt = isOpen && t.text ? t.text : (t.summary || t.name);
        const canExp = !!(t.text && t.text.length > 100);
        const workersHTML = (t.workers || []).map((w) => `<a class="btn small td-wk-pill" href="#/p/${encodeURIComponent(w.pane_id)}" title="查看 ${esc(w.name)} 终端"><span class="pi-dot ${w.status === "working" ? "pi-dot-run" : "pi-dot-need"}"></span>${esc(w.name)} · 看终端</a>`).join("");
        return `<div class="pi-card ${isOpen ? "is-open" : ""}">
          <div class="pi-avatar pi-av-timer">任</div>
          <div class="pi-body">
            <div class="pi-top">
              <div class="pi-title">${esc(t.title)}<span class="pi-tag">进行中任务</span></div>
              <span class="pi-time">${esc(fmtClock(t.mod))}</span>
            </div>
            <div class="pi-snippet">${renderRichSnippet(bodyTxt, esc)}</div>
            <div class="pi-foot">
              <span class="pi-status"><span class="pi-dot pi-dot-need"></span>任务进行中（完成后自动归档）</span>
              <span class="grow"></span>
              ${workersHTML}
              ${canExp ? `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-expand="${esc(key)}">${isOpen ? "收起" : "展开详情"}</button>` : ""}
              <button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-ask="${esc(`汇报一下当前任务「${t.title}」的最新进展和下一步`)}">催问进展</button>
            </div>
          </div>
        </div>`;
      }).join("");

      const todoById = new Map(todos.map((t) => [t.id, t]));
      const todoHTML = showTodos.map((t) => {
        const done = t.status === "done";
        const dispatched = t.status === "dispatched";
        const sel = todoBatchMode && todoSelected.has(t.id);
        const expKey = "td:" + t.id;
        const isOpen = inboxOpen.has(expKey);
        const canExp = !!(t.detail && (t.detail.length > 90 || t.detail.includes("\n")));
        const srcLab = t.source === "feishu" ? "飞书" : t.source === "agent" ? "助手" : "手动";
        const priTag = t.priority === "high"
          ? `<span class="pi-tag pi-pri-high">高优先</span>`
          : t.priority === "low" ? `<span class="pi-tag pi-pri-low">低</span>` : "";
        const statusDot = done ? "pi-dot-done" : dispatched ? "pi-dot-run" : "pi-dot-need";
        const statusLab = done
          ? "已完成"
          : dispatched
            ? `已派给 Swag${t.dispatched_at ? " · " + fmtClock(t.dispatched_at) : ""}`
            : "待处理";
        return `<div class="pi-card todo-card ${done ? "is-done" : dispatched ? "is-dispatched" : ""} ${sel ? "is-sel" : ""} ${isOpen ? "is-open" : ""}" data-td-id="${esc(t.id)}">
          ${todoBatchMode
            ? `<input type="checkbox" class="td-sel-chk" data-td-sel="${esc(t.id)}" ${sel ? "checked" : ""}>`
            : `<button type="button" class="todo-chk ${done ? "td-chk-done" : ""}" data-td-toggle="${esc(t.id)}" title="${done ? "标为未完成" : "标为已完成"}">${done ? "✓" : ""}</button>`}
          <div class="pi-body">
            <div class="pi-top">
              <div class="pi-title">${esc(t.title)}<span class="pi-tag">${esc(srcLab)}</span>${dispatched ? `<span class="pi-tag td-tag-run">已派发</span>` : ""}${priTag}${t.due ? `<span class="pi-tag">${esc(t.due)}</span>` : ""}</div>
              <span class="pi-time">${esc(fmtClock(t.updated_at || t.created_at))}</span>
            </div>
            ${t.detail ? `<div class="pi-snippet">${renderRichSnippet(t.detail, esc)}</div>` : ""}
            <div class="pi-foot">
              <span class="pi-status"><span class="pi-dot ${statusDot}"></span>${esc(statusLab)}</span>
              <span class="grow"></span>
              ${canExp ? `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-expand="${esc(expKey)}">${isOpen ? "收起" : "展开全文"}</button>` : ""}
              ${t.link ? `<a class="btn ghost small" style="height:24px;font-size:11.5px;text-decoration:none" href="${esc(t.link)}" target="_blank" rel="noopener">原链接</a>` : ""}
              ${!done && !dispatched && !todoBatchMode ? `<button type="button" class="btn small" style="height:24px;font-size:11.5px" data-td-dispatch="${esc(t.id)}">派给 Swag</button>` : ""}
              ${dispatched && !todoBatchMode ? `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-ask="${esc(`汇报一下已派待办 [todo:${t.id}]「${t.title}」的最新进展和结果`)}">催问进展</button><button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-reopen="${esc(t.id)}">退回待处理</button>` : ""}
              ${!todoBatchMode ? `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-del="${esc(t.id)}">删除</button>` : ""}
            </div>
          </div>
        </div>`;
      }).join("");

      const renderDocCard = (d, idx) => {
        const hasDoc = !!d.url;
        const isWeekly = !hasDoc && d.scope === "weekly";
        const isRollupDoc = hasDoc && d.doc_type === "周总复盘";
        const expKey = "doc:" + (d.task_file || d.url || idx);
        const isOpen = inboxOpen.has(expKey);
        const bodyTxt = isOpen && d.text ? d.text : (d.summary || "");
        const canExp = !!(d.text && (d.text.length > 100 || d.text !== d.summary));
        const genPrompt = d.task_file
          ? `请读取 memory/${d.task_file} 及相关验证证据，严格按 memory/notes/task-report-doc.md 规范为「${d.task_title || d.title}」创建一篇结构清晰、带对比表/可视化展示的飞书复盘文档并把链接发给我；创建后在 memory/${d.task_file} 末尾写回一行「复盘文档：[标题](文档链接)」。`
          : "";
        const rels = Array.isArray(d.related_tasks) ? d.related_tasks.filter(Boolean) : [];
        const footInfo = hasDoc
          ? (rels.length > 1
            ? `已合并 ${rels.length} 项任务：${rels.join(" · ")}`
            : `${isRollupDoc ? "周复盘主文档" : "已复盘"} · ${d.task_file || d.source || "对话汇报"}`)
          : isWeekly
            ? `已归入周复盘 · ${d.task_file || d.source || ""}`
            : `任务已归档 · ${d.task_file || d.source || ""}`;
        const avCls = isRollupDoc ? "pi-av-feishu" : hasDoc ? "pi-av-doc" : isWeekly ? "pi-av-sys" : "pi-av-miss";
        const avTxt = (isRollupDoc || isWeekly) ? "周" : hasDoc ? "文" : "完";
        const tagHTML = hasDoc
          ? `<span class="pi-tag">${esc(d.doc_type || "飞书文档")}</span>`
          : isWeekly
            ? `<span class="pi-tag">已归入周复盘</span>`
            : `<span class="pi-tag pi-pri-high">待补复盘</span>`;
        const wkTag = activeWeek === "all" && d.week ? `<span class="pi-tag">${esc(d.week)}</span>` : "";
        return `<div class="pi-card ${isOpen ? "is-open" : ""}">
          <div class="pi-avatar ${avCls}">${avTxt}</div>
          <div class="pi-body">
            <div class="pi-top">
              <div class="pi-title" title="${esc(d.title)}">${esc(d.title)}${tagHTML}${wkTag}</div>
              <span class="pi-time">${esc(fmtClock(d.updated_at))}</span>
            </div>
            ${bodyTxt && bodyTxt !== d.title ? `<div class="pi-snippet">${renderRichSnippet(bodyTxt, esc)}</div>` : ""}
            <div class="pi-foot">
              <span class="pi-status"><span class="pi-dot ${hasDoc || isWeekly ? "pi-dot-done" : "pi-dot-need"}"></span>${esc(footInfo)}</span>
              <span class="grow"></span>
              ${canExp ? `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-expand="${esc(expKey)}">${isOpen ? "收起" : "展开详情"}</button>` : ""}
              ${!hasDoc && d.task_file
                ? (isWeekly
                  ? `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-doc-scope="missing" data-td-doc-file="${esc(d.task_file)}">撤回为待补</button>`
                  : `<button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-td-doc-scope="weekly" data-td-doc-file="${esc(d.task_file)}">归入周复盘</button>`)
                : ""}
              ${hasDoc
                ? `<a class="btn small" style="height:24px;font-size:11.5px;text-decoration:none" href="${esc(d.url)}" target="_blank" rel="noopener">打开飞书复盘 ↗</a>`
                : (genPrompt ? `<button type="button" class="btn ${isWeekly ? "ghost" : ""} small" style="height:24px;font-size:11.5px" data-td-ask="${esc(genPrompt)}">${isWeekly ? "仍生成专项" : "生成专项复盘"}</button>` : "")}
            </div>
          </div>
        </div>`;
      };
      let docsHTML = "";
      if (activeWeek === "all" && weekList.length > 1) {
        const grouped = new Map();
        showDocs.forEach((d, idx) => {
          const wk = d.week || "未分周";
          if (!grouped.has(wk)) grouped.set(wk, []);
          grouped.get(wk).push({ d, idx });
        });
        docsHTML = [...grouped.entries()].map(([wk, rows]) => {
          const meta = weekMap.get(wk) || { key: wk, label: wk };
          return `<details class="pi-group" open>
            <summary><span>${esc(prettyWeek(meta))}</span><span class="muted">${rows.length} 项</span></summary>
            ${rows.map(({ d, idx }) => renderDocCard(d, idx)).join("")}
          </details>`;
        }).join("");
      } else {
        docsHTML = showDocs.map((d, idx) => renderDocCard(d, idx)).join("");
      }

      const openTotal = syncTodoBadge();
      const dispatchedTotal = todos.filter((t) => t.status === "dispatched").length;
      const doneTotal = todos.filter((t) => t.status === "done").length;
      const docsTotal = deliverables.length;
      const docChips = [
        ["all", `全部 (${weekScoped.length})`],
        ["weekly_doc", `周总复盘 (${activeWeek === "all" ? docsWeeklyDoc : scopedWeeklyDoc})`],
        ["special", `专项复盘 (${docsSpecialDoc})`],
        ["missing", `待补复盘 (${docsMissing})`],
        ["weekly", `已归入周复盘 (${docsWeekly})`],
      ];
      const weekBarHTML = weekList.length ? `<div class="pi-chips" style="margin-bottom:6px">${
        weekList.map((w) => `<button type="button" class="pi-chip ${activeWeek === w.key ? "on" : ""}" data-tdw="${esc(w.key)}">${esc(prettyWeek(w))} (${w.count})</button>`).join("")
      }<button type="button" class="pi-chip ${activeWeek === "all" ? "on" : ""}" data-tdw="all">全部周次 (${deliverables.length})</button></div>` : "";
      const srcChips = [
        ["all", "全部"],
        ["pending", `待审批 (${pending.length})`],
        ["task", `进行中任务 (${liveTasks.length})`],
        ["feishu", "飞书待办"],
        ["agent", "助手提案"],
        ["manual", "手动待办"],
      ];
      const visTodoIds = showTodos.map((t) => t.id);
      const selCount = todoSelected.size;
      const allVisSelected = visTodoIds.length > 0 && visTodoIds.every((id) => todoSelected.has(id));
      const batchBarHTML = todoBatchMode && todoFilter !== "docs" ? `<div class="td-batch-bar">
        <button type="button" class="btn small" id="td-sel-all">${allVisSelected ? "取消全选" : `全选当前 (${visTodoIds.length})`}</button>
        <span class="muted">已选 <b>${selCount}</b> 项</span>
        <span class="grow"></span>
        <button type="button" class="btn small" data-td-batch="done" ${selCount ? "" : "disabled"}>✓ 批量完成</button>
        ${todoFilter === "done" || todoFilter === "dispatched" ? `<button type="button" class="btn small" data-td-batch="open" ${selCount ? "" : "disabled"}>↺ 退回待处理</button>` : ""}
        ${todoFilter !== "done" ? `<button type="button" class="btn small" id="td-batch-dispatch" ${selCount ? "" : "disabled"}>⚡ 批量派给 Swag</button>` : ""}
        <button type="button" class="btn small" style="color:#ef4444" data-td-batch="delete" ${selCount ? "" : "disabled"}>批量删除</button>
      </div>` : "";

      body.innerHTML = `<div class="td-wrap">
        <div class="pi-bar">
          <div class="pi-tabs">
            <button type="button" class="pi-tab ${todoFilter === "open" ? "on" : ""}" data-tdf="open">待处理${openTotal ? `<span class="cnt">${openTotal}</span>` : ""}</button>
            <button type="button" class="pi-tab ${todoFilter === "dispatched" ? "on" : ""}" data-tdf="dispatched">已派发${dispatchedTotal ? ` <span class="muted">${dispatchedTotal}</span>` : ""}</button>
            <button type="button" class="pi-tab ${todoFilter === "done" ? "on" : ""}" data-tdf="done">已完成${doneTotal ? ` <span class="muted">${doneTotal}</span>` : ""}</button>
            <button type="button" class="pi-tab ${todoFilter === "docs" ? "on" : ""}" data-tdf="docs">任务复盘${docsTotal ? ` <span class="muted">${docsTotal}</span>` : ""}</button>
            <button type="button" class="pi-tab ${todoFilter === "all" ? "on" : ""}" data-tdf="all">全部</button>
          </div>
          <span class="grow"></span>
          ${todoFilter === "docs" && showDocs.length ? `<button type="button" class="btn small" id="td-copy-docs">复制 Markdown 清单</button>` : ""}
          ${todoFilter === "done" && doneTotal && !todoBatchMode ? `<button type="button" class="btn ghost small" id="td-clear-done">清空已完成</button>` : ""}
          ${todoFilter !== "docs" && todos.length ? `<button type="button" class="btn ${todoBatchMode ? "primary" : ""} small" id="td-batch-toggle">${todoBatchMode ? "完成管理" : "批量管理"}</button>` : ""}
          <button type="button" class="btn primary small" id="td-add">+ 新建待办</button>
        </div>
        ${batchBarHTML}
        <input type="search" id="td-search" class="pi-search" value="${esc(todoQuery)}" placeholder="${todoFilter === "docs" ? "搜索已完结任务、复盘文档、周次、来源…" : "搜索待办标题、详情、来源…"}">
        ${todoFilter === "docs"
          ? `${weekBarHTML}<div class="pi-chips">${docChips.map(([k, l]) => `<button type="button" class="pi-chip ${docFilter === k ? "on" : ""}" data-tdd="${k}">${l}</button>`).join("")}</div>`
          : `<div class="pi-chips">${srcChips.map(([k, l]) => `<button type="button" class="pi-chip ${todoSrc === k ? "on" : ""}" data-tds="${k}">${l}</button>`).join("")}</div>`}
        <div class="pi-list">
          ${todoFilter === "docs"
            ? (docsHTML || `<div class="pool-empty">当前筛选下没有已完结任务或复盘文档</div>`)
            : `${pendHTML}${taskHTML}${todoHTML}${!(pendHTML || taskHTML || todoHTML) ? `<div class="pool-empty">当前筛选下没有待办事项 🎉</div>` : ""}`}
        </div>
      </div>`;

      body.scrollTop = prevScroll;
      if (activeSearch) {
        const s = body.querySelector("#td-search");
        if (s) { s.focus(); if (prevPos != null) s.setSelectionRange(prevPos, prevPos); }
      }

      const runBatch = async (ids, action, silent = false) => {
        if (!ids.length) return;
        const idSet = new Set(ids);
        const nowISO = new Date().toISOString();
        if (action === "delete") {
          todos = todos.filter((t) => !idSet.has(t.id));
        } else {
          todos = todos.map((t) => (idSet.has(t.id)
            ? { ...t, status: action, ...(action === "dispatched" ? { dispatched_at: nowISO } : {}), updated_at: nowISO }
            : t));
        }
        ids.forEach((id) => todoSelected.delete(id));
        paint();
        try {
          await api("/api/super/todos/batch", { body: { ids, action } });
          if (!silent) {
            toast(
              action === "delete" ? `已批量删除 ${ids.length} 条待办`
                : action === "done" ? `已批量完成 ${ids.length} 条待办`
                : action === "dispatched" ? `已派给 Swag（${ids.length} 条移入「已派发」）`
                : `已退回待处理（${ids.length} 条）`,
            );
          }
        } catch (err) {
          toast(err.message, "attn");
          silentReload();
        }
      };

      const dispatchTodos = (items, clearBatch = false) => {
        openDispatchModal(items, esc, toast, async (msg, sentList) => {
          if (clearBatch) { todoBatchMode = false; todoSelected.clear(); }
          const ids = (sentList || items || []).map((t) => t.id).filter(Boolean);
          if (ids.length) await runBatch(ids, "dispatched", true);
          if (onDispatch) onDispatch(msg);
        });
      };

      const wrap = body.querySelector(".td-wrap");
      wrap.addEventListener("change", (e) => {
        const chk = e.target.closest("[data-td-sel]");
        if (chk) {
          const id = chk.dataset.tdSel;
          if (chk.checked) todoSelected.add(id); else todoSelected.delete(id);
          paint();
        }
      });
      wrap.addEventListener("click", async (e) => {
        const tf = e.target.closest("[data-tdf]");
        if (tf) { todoFilter = tf.dataset.tdf; paint(); return; }
        const ts = e.target.closest("[data-tds]");
        if (ts) { todoSrc = ts.dataset.tds; paint(); return; }
        const td = e.target.closest("[data-tdd]");
        if (td) {
          docFilter = td.dataset.tdd;
          if (docFilter === "weekly_doc" && weekList.length > 1) docWeek = "all";
          paint();
          return;
        }
        const tw = e.target.closest("[data-tdw]");
        if (tw) { docWeek = tw.dataset.tdw; paint(); return; }
        const scBtn = e.target.closest("[data-td-doc-scope]");
        if (scBtn) {
          const tf = scBtn.dataset.tdDocFile;
          const sc = scBtn.dataset.tdDocScope;
          deliverables = deliverables.map((d) => (d.task_file === tf && !d.url
            ? { ...d, scope: sc, doc_type: sc === "weekly" ? "已归入周复盘" : "待补复盘" }
            : d));
          paint();
          try {
            await api("/api/super/deliverables/scope", { body: { task_file: tf, scope: sc } });
            toast(sc === "weekly" ? "已归入周复盘" : "已撤回为待补复盘");
          } catch (err) {
            toast(err.message, "attn");
            silentReload();
          }
          return;
        }
        if (e.target.closest("#td-batch-toggle")) {
          todoBatchMode = !todoBatchMode;
          if (!todoBatchMode) todoSelected.clear();
          paint();
          return;
        }
        if (e.target.closest("#td-sel-all")) {
          if (allVisSelected) visTodoIds.forEach((id) => todoSelected.delete(id));
          else visTodoIds.forEach((id) => todoSelected.add(id));
          paint();
          return;
        }
        if (e.target.closest("#td-clear-done")) {
          const doneIds = todos.filter((t) => t.status === "done").map((t) => t.id);
          if (doneIds.length && await openConfirmModal({
            title: `清空 ${doneIds.length} 条已完成待办`,
            message: "清空后将永久移除这些已完成记录，无法恢复。",
            confirmText: "确认清空",
            danger: true,
          }, esc)) runBatch(doneIds, "delete");
          return;
        }
        const batchBtn = e.target.closest("[data-td-batch]");
        if (batchBtn) {
          const ids = [...todoSelected];
          const act = batchBtn.dataset.tdBatch;
          if (act === "delete" && !(await openConfirmModal({
            title: `删除选中的 ${ids.length} 条待办`,
            message: "删除后无法恢复，确定继续？",
            confirmText: "确认删除",
            danger: true,
          }, esc))) return;
          runBatch(ids, act);
          return;
        }
        if (e.target.closest("#td-batch-dispatch")) {
          const items = [...todoSelected].map((id) => todoById.get(id)).filter(Boolean);
          dispatchTodos(items, true);
          return;
        }
        const disp = e.target.closest("[data-td-dispatch]");
        if (disp) {
          const item = todoById.get(disp.dataset.tdDispatch);
          if (item) dispatchTodos([item], false);
          return;
        }
        const reopen = e.target.closest("[data-td-reopen]");
        if (reopen) {
          const id = reopen.dataset.tdReopen;
          todos = todos.map((t) => (t.id === id ? { ...t, status: "open", updated_at: new Date().toISOString() } : t));
          paint();
          try { await api(`/api/super/todos/${encodeURIComponent(id)}`, { body: { status: "open" } }); toast("已退回待处理"); }
          catch (err) { toast(err.message, "attn"); silentReload(); }
          return;
        }
        const exp = e.target.closest("[data-td-expand]");
        if (exp) {
          const k = exp.dataset.tdExpand;
          if (inboxOpen.has(k)) inboxOpen.delete(k); else inboxOpen.add(k);
          paint();
          return;
        }
        const rep = e.target.closest("[data-td-reply-opt]");
        if (rep && onDispatch) {
          onDispatch(rep.dataset.tdReplyOpt, { entry: rep.dataset.tdReplyRe });
          return;
        }
        const ask = e.target.closest("[data-td-ask]");
        if (ask && onDispatch) { onDispatch(ask.dataset.tdAsk); return; }
        const tog = e.target.closest("[data-td-toggle]");
        if (tog) {
          const id = tog.dataset.tdToggle;
          const prev = todos.find((t) => t.id === id);
          if (!prev) return;
          const nextStatus = prev.status === "done" ? "open" : "done";
          todos = todos.map((t) => (t.id === id ? { ...t, status: nextStatus, updated_at: new Date().toISOString() } : t));
          paint();
          try { await api(`/api/super/todos/${encodeURIComponent(id)}`, { body: { status: nextStatus } }); }
          catch (err) { toast(err.message, "attn"); silentReload(); }
          return;
        }
        const del = e.target.closest("[data-td-del]");
        if (del) {
          const id = del.dataset.tdDel;
          todos = todos.filter((t) => t.id !== id);
          paint();
          try { await api(`/api/super/todos/${encodeURIComponent(id)}`, { body: { delete: true } }); toast("已删除待办"); }
          catch (err) { toast(err.message, "attn"); silentReload(); }
          return;
        }
        if (e.target.closest("#td-copy-docs")) {
          const head = activeWeek !== "all" && weekMap.has(activeWeek) ? `## ${prettyWeek(weekMap.get(activeWeek))} 复盘清单\n` : "";
          const md = head + showDocs.map((d, i) => d.url
            ? `${i + 1}. [${d.title}](${d.url})${d.summary ? ` — ${d.summary}` : ""}`
            : `${i + 1}. ${d.title}（${d.scope === "weekly" ? "归入周复盘" : "待补复盘"}）${d.summary ? ` — ${d.summary}` : ""}`).join("\n");
          try { await navigator.clipboard.writeText(md); toast(`已复制 ${showDocs.length} 条任务复盘 Markdown 清单`); }
          catch { toast("复制失败，请手动复制", "attn"); }
          return;
        }
        if (e.target.closest("#td-add")) {
          openTodoModal({ modalTitle: "新建待办", source: todoSrc === "feishu" ? "feishu" : "manual", priority: "medium" }, api, esc, toast, {
            onDispatch,
            onSaved: () => silentReload(),
          });
        }
      });
      wrap.querySelector("#td-search")?.addEventListener("input", (e) => {
        todoQuery = e.target.value || "";
        paint();
      });
    };
    paint();
  }

  function syncIdleReapButton(btn, workers, agents, api, esc, toast, onReaped) {
    if (!btn) return;
    const activeSwag = (workers || []).filter((w) => !w.ended);
    const swagPanes = new Set(activeSwag.map((w) => w.pane_id).filter(Boolean));
    const swagNames = new Set(["swag", "swag-dream", ...activeSwag.map((w) => w.name).filter(Boolean)]);
    const idleSwag = activeSwag.filter((w) => w.status === "done" || w.status === "idle");
    const idleManual = (agents || []).filter((a) => (a.status === "done" || a.status === "idle") && !swagNames.has(a.name) && !swagPanes.has(a.pane_id));
    const total = idleSwag.length + idleManual.length;
    btn.hidden = total === 0;
    btn.textContent = total ? `清理空闲 (${total})` : "清理空闲";
    btn._idleSwag = idleSwag.length;
    btn._idleManual = idleManual.length;
    if (btn._reapBound) return;
    btn._reapBound = true;
    btn.addEventListener("click", async () => {
      const ns = btn._idleSwag || 0, nm = btn._idleManual || 0, n = ns + nm;
      if (!n) return;
      const parts = [];
      if (ns) parts.push(`${ns} 个 Swag 派出会话`);
      if (nm) parts.push(`${nm} 个手动打开的空闲终端`);
      if (!(await openConfirmModal({
        title: `归档 ${n} 个空闲会话`,
        message: `将关闭 ${parts.join("和")}。正在运行/阻塞中的任务不受影响；关闭后对话记录保留在下方「最近」中，可随时恢复。`,
        confirmText: "确认归档",
      }, esc))) return;
      btn.disabled = true;
      try {
        const r = await api("/api/super/workers/reap", { body: { include_manual: nm > 0 } });
        const cnt = r.count || 0;
        toast(cnt ? `已归档 ${cnt} 个空闲会话（记录已保留）` : "没有可归档的空闲会话");
        if (onReaped) onReaped();
      } catch (err) {
        toast(err.message, "attn");
      } finally {
        btn.disabled = false;
      }
    });
  }

  function mountRolesSection(box, initRoles, api, esc, toast) {
    if (!box) return;
    let roles = { ...(initRoles || {}) };

    const persistRoles = async (nextRoles, msg) => {
      const cur = (await api("/api/super/settings")).settings || {};
      await api("/api/super/settings", { body: { settings: { ...cur, roles: nextRoles } } });
      roles = { ...nextRoles };
      paint();
      toast(msg || "已保存角色配置");
    };

    const openRoleModal = (key, isNew = false) => {
      const orig = isNew
        ? { name: "", description: "", group: "compute", cwd: "", kind: "codex", model: "", effort: "", auth_profile: "", ask_permissions: false }
        : (roles[key] || { name: key });
      const knownProfiles = [...new Set(["swag-pool", ...Object.values(roles).map((r) => r?.auth_profile).filter(Boolean), orig.auth_profile].filter(Boolean))];
      const bg = document.createElement("div");
      bg.className = "pool-modal-bg";
      bg.innerHTML = `<div class="pool-modal" role="dialog" aria-modal="true">
        <div class="pool-modal-head">
          <h3>${isNew ? "新建 Agent 角色" : `编辑角色 · ${esc(key)}`}</h3>
          <button type="button" class="pool-ico-btn" data-rm-close title="关闭">✕</button>
        </div>
        <div class="pool-modal-body">
          <div class="pool-sec">
            <div class="pool-sec-h"><span>基础信息</span></div>
            <div class="pool-form-grid">
              <label class="pool-field">角色名（唯一标识）<input data-rf="name" class="mono" value="${esc(orig.name || key || "")}" placeholder="如 deploy-ops" ${isNew ? "" : "readonly"}></label>
              <label class="pool-field">机器分组<select data-rf="group">${[["compute", "compute（算力机）"], ["hub", "hub（中控机）"], ["local", "local（本机）"], ["", "不限（跟默认）"]].map(([v, l]) => `<option value="${v}" ${(orig.group || "") === v ? "selected" : ""}>${l}</option>`).join("")}</select></label>
            </div>
            <label class="pool-field">职责描述（Swag Router 派活与展示用）<input data-rf="description" value="${esc(orig.description || "")}" placeholder="一句话说明该角色负责什么任务、优先用什么工具"></label>
            <label class="pool-field">默认工作目录（可选）<input data-rf="cwd" class="mono" value="${esc(orig.cwd || "")}" placeholder="~/Projects/... 或绝对路径"></label>
          </div>
          <div class="pool-sec">
            <div class="pool-sec-h"><span>引擎与模型</span></div>
            <div class="pool-form-grid">
              <label class="pool-field">Agent 引擎<select data-rf="kind">${[["codex", "Codex"], ["claude", "Claude"], ["antigravity", "Antigravity"]].map(([v, l]) => `<option value="${v}" ${(orig.kind || "codex") === v ? "selected" : ""}>${l}</option>`).join("")}</select></label>
              <label class="pool-field">模型 / 档位<select data-rf="model" class="mono"><option value="${esc(orig.model || "")}" selected>${esc(orig.model || "默认（读取中…）")}</option></select></label>
              <label class="pool-field">思考强度<select data-rf="effort">${[["", "默认"], ["low", "低"], ["medium", "中"], ["high", "高"], ["xhigh", "很高"], ["max", "最高"]].map(([v, l]) => `<option value="${v}" ${(orig.effort || "") === v ? "selected" : ""}>${l}</option>`).join("")}</select></label>
              <label class="pool-field">凭据方案（Auth Profile）<select data-rf="auth_profile" class="mono"><option value="" ${!orig.auth_profile ? "selected" : ""}>原生默认（不走反代池）</option>${knownProfiles.map((p) => `<option value="${esc(p)}" ${orig.auth_profile === p ? "selected" : ""}>${esc(p === "swag-pool" ? "swag-pool（模型池反代）" : p)}</option>`).join("")}<option value="__custom">其他（手动输入）…</option></select></label>
            </div>
            <label class="check" style="margin-top:8px"><input type="checkbox" data-rf="ask_permissions" ${orig.ask_permissions ? "checked" : ""}> 启用高风险操作审批提醒（ask_permissions）</label>
          </div>
        </div>
        <div class="pool-modal-foot">
          ${!isNew && key !== "router" ? `<button type="button" class="btn ghost small" id="rm-del" style="color:#ef4444">删除角色</button>` : ""}
          <span class="grow"></span>
          <button type="button" class="btn small" data-rm-close>取消</button>
          <button type="button" class="btn primary small" id="rm-save">保存角色</button>
        </div>
      </div>`;
      const kindSel = bg.querySelector("[data-rf=kind]");
      const modelSel = bg.querySelector("[data-rf=model]");
      const profSel = bg.querySelector("[data-rf=auth_profile]");
      const loadRoleModels = async (wantVal) => {
        const k = kindSel?.value || "codex";
        const [mRes, qRes] = await Promise.all([
          api("/api/models?kind=" + encodeURIComponent(k)).catch(() => ({ models: [] })),
          api("/api/super/quota").catch(() => ({ pool: { tiers: [] } })),
        ]);
        if (!bg.isConnected) return;
        const tiers = (qRes?.pool?.tiers || []).map((t) => ({ id: t.id, label: `${t.id}${t.label ? " · " + t.label : ""}` }));
        const native = (mRes?.models || []).map((m) => ({ id: m.id, label: m.label && m.label !== m.id ? `${m.label} (${m.id})` : m.id }));
        const seen = new Set();
        const opts = [];
        const add = (id, label) => { if (id && !seen.has(id)) { seen.add(id); opts.push({ id, label }); } };
        tiers.forEach((t) => add(t.id, t.label));
        native.forEach((m) => add(m.id, m.label));
        if (wantVal && !seen.has(wantVal)) opts.unshift({ id: wantVal, label: wantVal });
        modelSel.innerHTML = `<option value="" ${!wantVal ? "selected" : ""}>默认（跟随全局/原生）</option>`
          + opts.map((o) => `<option value="${esc(o.id)}" ${o.id === wantVal ? "selected" : ""}>${esc(o.label)}</option>`).join("")
          + `<option value="__custom">其他（手动输入）…</option>`;
      };
      loadRoleModels(orig.model || "");
      kindSel?.addEventListener("change", () => loadRoleModels(modelSel?.value === "__custom" ? "" : (modelSel?.value || "")));
      const bindCustomOpt = (sel, title, placeholder) => {
        sel?.addEventListener("change", async () => {
          if (sel.value !== "__custom") return;
          const v = await openInputModal({ title, label: title, placeholder }, esc);
          if (v) {
            const opt = new Option(v, v, true, true);
            sel.add(opt, sel.options.length - 1);
          } else {
            sel.value = "";
          }
        });
      };
      bindCustomOpt(modelSel, "手动输入模型名 / 档位", "如 gpt-6.2-astra 或 tier-standard");
      bindCustomOpt(profSel, "手动输入凭据方案名", "如 swag-pool");
      const close = () => { document.removeEventListener("keydown", onKey); bg.remove(); };
      const onKey = (ev) => { if (ev.key === "Escape") close(); };
      document.addEventListener("keydown", onKey);
      document.body.appendChild(bg);
      bg.addEventListener("click", async (ev) => {
        if (ev.target === bg || ev.target.closest("[data-rm-close]")) { close(); return; }
        if (ev.target.closest("#rm-del")) {
          if (!(await openConfirmModal({ title: `删除角色「${key}」`, message: "删除后定时任务和派活不再使用该角色配置，确认删除？", confirmText: "确认删除", danger: true }, esc))) return;
          const next = { ...roles }; delete next[key];
          try { await persistRoles(next, `已删除角色 ${key}`); close(); } catch (err) { toast(err.message, "attn"); }
          return;
        }
        if (ev.target.closest("#rm-save")) {
          const val = (f) => bg.querySelector(`[data-rf=${f}]`)?.value?.trim() || "";
          const name = isNew ? val("name") : key;
          if (!name || !/^[a-zA-Z0-9._-]+$/.test(name)) { toast("角色名必填，仅支持字母、数字、点、横线和下划线", "attn"); return; }
          const rec = {
            ...(roles[name] || {}),
            name,
            description: val("description"),
            group: val("group"),
            cwd: val("cwd"),
            kind: val("kind") || "codex",
            model: val("model") === "__custom" ? "" : val("model"),
            effort: val("effort"),
            auth_profile: val("auth_profile") === "__custom" ? "" : val("auth_profile"),
            ask_permissions: !!bg.querySelector("[data-rf=ask_permissions]")?.checked,
          };
          if (!rec.cwd) delete rec.cwd;
          if (!rec.model) delete rec.model;
          if (!rec.effort) delete rec.effort;
          if (!rec.auth_profile) delete rec.auth_profile;
          if (!rec.ask_permissions) delete rec.ask_permissions;
          const btn = ev.target.closest("#rm-save");
          btn.disabled = true;
          try { await persistRoles({ ...roles, [name]: rec }, isNew ? `已新建角色 ${name}` : `已更新角色 ${name}`); close(); }
          catch (err) { toast(err.message, "attn"); btn.disabled = false; }
        }
      });
    };

    const paint = () => {
      const list = Object.entries(roles).sort(([a], [b]) => (a === "router" ? 1 : b === "router" ? -1 : a.localeCompare(b)));
      const rows = list.map(([k, r]) => {
        const tags = [
          r.group ? `组: ${r.group}` : "",
          r.kind || "codex",
          r.model || "默认模型",
          r.auth_profile ? `通道: ${r.auth_profile}` : "原生默认",
          r.effort ? `思考: ${r.effort}` : "",
          r.ask_permissions ? "高风险审批" : "",
        ].filter(Boolean);
        return `<div class="pi-card" style="padding:10px 12px;margin-bottom:8px">
          <div class="pi-body">
            <div class="pi-top">
              <div class="pi-title"><span class="mono">${esc(k)}</span>${tags.map((t) => `<span class="pi-tag">${esc(t)}</span>`).join("")}</div>
              <span style="display:inline-flex;gap:6px">
                <button type="button" class="btn ghost small" style="height:24px;font-size:11.5px" data-role-edit="${esc(k)}">编辑</button>
              </span>
            </div>
            ${r.description ? `<div class="pi-snippet" style="-webkit-line-clamp:3">${esc(r.description)}</div>` : ""}
            ${r.cwd ? `<div class="muted mono" style="font-size:11.5px;margin-top:4px">目录：${esc(r.cwd)}</div>` : ""}
          </div>
        </div>`;
      }).join("");
      box.innerHTML = `<header style="display:flex;align-items:flex-start;justify-content:space-between;gap:12px">
        <div><h4>Agent 角色清单（${list.length}）</h4><p>Swag 派活和定时任务可直接按 <code>--role 角色名</code> 自动路由到对应机器分组、目录与模型档位；角色行为说明位于「自动化 → 记忆 → notes/agents.md」。</p></div>
        <button type="button" class="btn small" id="role-add-btn" style="flex:none">+ 新建角色</button>
      </header>
      <div style="margin-top:10px">${rows || `<div class="pool-empty">还没有定义 Agent 角色</div>`}</div>`;
      box.querySelector("#role-add-btn")?.addEventListener("click", () => openRoleModal("", true));
      box.querySelectorAll("[data-role-edit]").forEach((b) => b.addEventListener("click", () => openRoleModal(b.dataset.roleEdit, false)));
    };
    paint();
  }

  function openMemoryImportModal(api, esc, toast, onImported) {
    document.querySelector(".pool-modal-bg[data-mem-import]")?.remove();
    const bg = document.createElement("div");
    bg.className = "pool-modal-bg";
    bg.dataset.memImport = "1";
    let pickedFile = null;
    let mode = "merge";
    bg.innerHTML = `<div class="pool-modal" style="max-width:520px">
      <div class="pool-modal-h"><h3>导入记忆 ZIP</h3><button type="button" class="icon-btn" data-mi-close title="关闭">✕</button></div>
      <div class="pool-modal-b">
        <p class="muted" style="margin:0 0 10px;font-size:12.5px">选择之前导出的 <code>swag-memory-*.zip</code>，先预览变更差异再确认写入；导入后会自动同步到远程 Router 与只读镜像。</p>
        <div class="pm-grid" style="margin-bottom:10px">
          <label class="pm-f"><span>导入方式</span><select id="mi-mode"><option value="merge">合并导入（保留本地独有文件，覆盖同名更新）</option><option value="replace">覆盖导入（与压缩包完全对齐，删除多余文件）</option></select></label>
          <label class="pm-f"><span>选择 ZIP 文件</span><input type="file" id="mi-file" accept=".zip,application/zip"></label>
        </div>
        <div id="mi-preview" class="pool-empty" style="text-align:left;padding:10px 12px;font-size:12px">请选择一个 ZIP 文件以预览变更</div>
      </div>
      <div class="pool-modal-f">
        <span class="grow"></span>
        <button type="button" class="btn small" data-mi-close>取消</button>
        <button type="button" class="btn primary small" id="mi-apply" disabled>确认导入</button>
      </div>
    </div>`;
    const close = () => { document.removeEventListener("keydown", onEsc); bg.remove(); };
    const onEsc = (ev) => { if (ev.key === "Escape") close(); };
    document.addEventListener("keydown", onEsc);
    document.body.appendChild(bg);

    const previewEl = bg.querySelector("#mi-preview");
    const applyBtn = bg.querySelector("#mi-apply");
    const runReq = async (dryRun) => {
      if (!pickedFile) return;
      const fd = new FormData();
      fd.append("file", pickedFile);
      const url = `/api/super/memory/import?mode=${encodeURIComponent(mode)}${dryRun ? "&dry_run=1" : ""}`;
      const r = await fetch(url, { method: "POST", body: fd, credentials: "same-origin" });
      const data = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
      return data;
    };
    const preview = async () => {
      if (!pickedFile) { applyBtn.disabled = true; return; }
      applyBtn.disabled = true;
      previewEl.innerHTML = "正在校验并预览压缩包差异…";
      try {
        const res = await runReq(true);
        const list = (arr, label, cls) => arr.length
          ? `<div style="margin-top:6px"><b class="${cls}">${label}（${arr.length}）</b>：<span class="mono">${esc(arr.slice(0, 12).join("、"))}${arr.length > 12 ? " …" : ""}</span></div>`
          : "";
        previewEl.innerHTML = `<div>模式：<b>${mode === "replace" ? "覆盖对齐" : "合并更新"}</b> · 未变化 ${res.unchanged.length} 个文件</div>`
          + list(res.added, "新增", "ok")
          + list(res.updated, "更新", "warn")
          + list(res.removed, "将删除", "bad")
          + (res.skipped?.length ? `<div class="muted" style="margin-top:6px">跳过非记忆文件 ${res.skipped.length} 个</div>` : "");
        applyBtn.disabled = false;
      } catch (err) {
        previewEl.innerHTML = `<span class="bad">校验失败：${esc(err.message)}</span>`;
      }
    };

    bg.addEventListener("click", (ev) => { if (ev.target === bg || ev.target.closest("[data-mi-close]")) close(); });
    bg.querySelector("#mi-mode").addEventListener("change", (ev) => { mode = ev.target.value; preview(); });
    bg.querySelector("#mi-file").addEventListener("change", (ev) => { pickedFile = ev.target.files?.[0] || null; preview(); });
    applyBtn.addEventListener("click", async () => {
      applyBtn.disabled = true;
      applyBtn.textContent = "导入中…";
      try {
        const res = await runReq(false);
        close();
        toast(`记忆导入完成：新增 ${res.added.length}、更新 ${res.updated.length}` + (res.removed.length ? `、移除 ${res.removed.length}` : ""));
        onImported?.();
      } catch (err) {
        toast(err.message, "attn");
        applyBtn.disabled = false;
        applyBtn.textContent = "确认导入";
      }
    });
  }

  function renderLiveList(ul, live, expanded, onToggle) {
    if (!ul) return;
    if (!ul._liveBound) {
      ul._liveBound = true;
      ul.addEventListener("click", (ev) => {
        if (!ev.target.closest("[data-act=live-more]")) return;
        ev.preventDefault();
        ul._onLiveToggle?.();
      });
    }
    ul._onLiveToggle = () => onToggle(!expanded);
    if (!live.length) {
      ul.innerHTML = `<li class="empty-hint">没有在跑的会话。点右上角 + 新建，或让 Swag 派一个。</li>`;
      return;
    }
    const LIVE_N = 8;
    let shown = expanded || live.length <= LIVE_N ? live : live.slice(0, LIVE_N);
    if (!expanded && live.length > LIVE_N) {
      const cur = live.slice(LIVE_N).find((x) => x.on);
      if (cur) shown = [...shown, cur];
    }
    const hiddenN = live.length - shown.length;
    const more = live.length > LIVE_N && (expanded || hiddenN > 0)
      ? `<li><button type="button" class="more-row" data-act="live-more">${expanded ? "收起" : `展开其余 ${hiddenN} 个`}</button></li>`
      : "";
    ul.innerHTML = shown.map((x) => x.html).join("") + more;
  }

  window.SwagPanels = { renderUsage, renderPool, renderInbox, renderTodos, recallMsg, syncIdleReapButton, mountRolesSection, renderLiveList, openMemoryImportModal };
})();
