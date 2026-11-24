(() => {
  const dict = {
    en: {
      "nav.arch": "Architecture",
      "nav.features": "Capabilities",
      "nav.start": "Quick Start",
      "hero.eyebrow": "Single Go Binary · Self-Hosted · MIT",
      "hero.h1a": "One control deck for all your ",
      "hero.h1b": "coding agents",
      "hero.h1c": " across every machine.",
      "hero.lede": "Swag connects always-on servers, GPU workstations, and your laptop over outbound yamux tunnels into one Super Agent workspace—with server-computed Inbox & Todo triage, an OpenAI-compatible failover model pool, and in-place Feishu card approvals.",
      "hero.cta.gh": "View on GitHub",
      "hero.cta.start": "3-Min Setup",
      "arch.kicker": "Architecture",
      "arch.h": "Zero inbound ports on your machines. One authoritative Hub.",
      "arch.p": "Your laptops and compute boxes dial outbound to the Hub over multiplexed yamux streams. State, SQLite WAL history, memory, and model failover stay centralized—while agents run natively next to your code.",
      "feat.kicker": "Core Capabilities",
      "feat.h": "Built for daily multi-machine engineering, not toy demos.",
      "feat.p": "Every subsystem is engineered as a small, verifiable Go boundary with zero frontend build pipeline.",
      "f1.t": "Multi-Machine Fleet & Role Isolation",
      "f1.s": "Dispatch Claude Code, Codex, or Antigravity workers by machine group or role profile, with 0600 isolated auth profiles and seamless session resume.",
      "f2.t": "Server-Computed Inbox & Todo Center",
      "f2.s": "Inbox triages incoming events while Todo tracks committed actions. Badges are projected on the Hub so sidebar, tab, and list counts never drift.",
      "f3.t": "Model Pool Proxy & Native Subscription Fallback",
      "f3.s": "Expose one /v1 endpoint with tier-fast, tier-standard, and tier-strong failover chains, automatic rate-limit cooldowns, and fallback to native CLI subscriptions.",
      "f4.t": "Persistent Memory, Dream & Feishu Card Callbacks",
      "f4.s": "Nightly Dream compacts long-term memory and mirrors it read-only across nodes. Approval cards update in place via Feishu card.action.trigger WebSockets.",
      "qs.kicker": "Quick Start",
      "qs.h": "Single binary. Zero Node build step.",
      "qs.p": "Compile once on Go 1.24+, pair your phone or compute nodes with one-time QR codes, and keep all data inside ~/.swag/.",
      "qs.copy": "Copy",
      "qs.copied": "Copied",
      "foot.copy": "MIT License · Built in Go & vanilla Web",
    },
    zh: {
      "nav.arch": "架构",
      "nav.features": "核心能力",
      "nav.start": "快速开始",
      "hero.eyebrow": "单二进制 Go 服务 · 自托管 · MIT 开源",
      "hero.h1a": "把多台电脑上的 ",
      "hero.h1b": "AI Agent",
      "hero.h1c": " 收进同一个控制台。",
      "hero.lede": "Swag 用出站 yamux 隧道把常驻服务器、算力节点与个人笔记本连成一套个人 Super Agent 调度底座——内置服务端统一的收件箱与待办中心、OpenAI 兼容多通道故障切换模型池，以及飞书卡片原位审批回调。",
      "hero.cta.gh": "前往 GitHub",
      "hero.cta.start": "3 分钟部署",
      "arch.kicker": "架构设计",
      "arch.h": "节点零入站端口，Hub 持有唯一权威状态。",
      "arch.p": "Mac 笔记本与远程开发机主动出站连回 Hub；SQLite WAL 对话历史、长期记忆、收件箱投影与模型反代池集中在 Hub，Agent 则原地运行在你的代码与环境旁边。",
      "feat.kicker": "核心能力",
      "feat.h": "面向真实多机研发日常，而非玩具 Demo。",
      "feat.p": "Go 单二进制内嵌零构建原生前端，每个核心模块都有清晰边界与自动化架构守卫测试。",
      "f1.t": "多机派活池与角色凭证隔离",
      "f1.s": "按机器分组或角色模板派发 Claude Code / Codex / Antigravity 任务，支持 0600 独立凭证注入、断线无缝续跑与终端直连。",
      "f2.t": "服务端统一收件箱与待办中心",
      "f2.s": "收件箱负责外部事件分诊，待办中心沉淀明确行动项。Hub 统一投影未读角标，侧栏、Tab 与列表计数严格一致，支持一键转待办与 swag todo CLI。",
      "f3.t": "模型反代池与原生订阅兜底",
      "f3.s": "提供统一 /v1/responses 与 /v1/chat/completions 入口，支持 tier-fast / tier-standard / tier-strong 故障切换、限流自动冷却、原生 CLI 订阅兜底与跨机 Token 用量统计。",
      "f4.t": "长期记忆、夜间做梦与飞书原位审批",
      "f4.s": "夜间 Dream 自动整理记忆并同步只读镜像到各节点；阻塞或审批卡片支持通过飞书 card.action.trigger 长连接原地变绿确认，不再跳转浏览器。",
      "qs.kicker": "快速开始",
      "qs.h": "单个 Go 二进制，零前端打包依赖。",
      "qs.p": "Go 1.24+ 直接编译，扫码配对手机 PWA 或一键接入新电脑节点，所有状态与密钥留在你自己的 ~/.swag/ 目录。",
      "qs.copy": "复制命令",
      "qs.copied": "已复制",
      "foot.copy": "MIT 协议 · Go + 原生 Web 构建",
    },
  };

  let lang = "zh";
  const applyLang = () => {
    const map = dict[lang];
    document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
    document.querySelectorAll("[data-i18n]").forEach((el) => {
      const v = map[el.dataset.i18n];
      if (v) el.textContent = v;
    });
    const lb = document.getElementById("lang-toggle");
    if (lb) lb.textContent = lang === "zh" ? "EN" : "中文";
  };

  document.getElementById("lang-toggle")?.addEventListener("click", () => {
    lang = lang === "zh" ? "en" : "zh";
    applyLang();
  });

  // Theme toggle
  const themeBtn = document.getElementById("theme-toggle");
  const getTheme = () => document.documentElement.dataset.theme || (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
  const syncThemeBtn = () => {
    if (themeBtn) themeBtn.textContent = getTheme() === "dark" ? "☀" : "☾";
  };
  themeBtn?.addEventListener("click", () => {
    document.documentElement.dataset.theme = getTheme() === "dark" ? "light" : "dark";
    syncThemeBtn();
  });
  syncThemeBtn();

  // Sticky topbar border
  const topbar = document.querySelector(".topbar");
  const onScroll = () => topbar?.classList.toggle("scrolled", window.scrollY > 12);
  window.addEventListener("scroll", onScroll, { passive: true });
  onScroll();

  // Interactive Control Deck tabs
  const navBtns = document.querySelectorAll("[data-deck-tab]");
  const views = document.querySelectorAll("[data-deck-view]");
  const titleEl = document.getElementById("dm-title");
  const subEl = document.getElementById("dm-sub");
  const metaMap = {
    chat: ["和 Swag 对话", "Router 在线 · 3/3 节点已连接"],
    inbox: ["收件箱", "服务端统一投影 · 需要你分诊的外部信号"],
    todos: ["待办中心", "待审批 · 进行中任务 · 结构化待办"],
    pool: ["模型池 & 原生配额", "统一反代 /v1 · 自动冷却与订阅兜底"],
  };
  const switchDeck = (tab) => {
    navBtns.forEach((b) => b.classList.toggle("on", b.dataset.deckTab === tab));
    views.forEach((v) => v.classList.toggle("on", v.dataset.deckView === tab));
    if (metaMap[tab] && titleEl && subEl) {
      titleEl.textContent = metaMap[tab][0];
      subEl.textContent = metaMap[tab][1];
    }
  };
  navBtns.forEach((b) => b.addEventListener("click", () => switchDeck(b.dataset.deckTab)));

  // Interactive Feishu card + Chat approval sync demo
  const larkCard = document.getElementById("lark-float");
  const chatConfirm = document.getElementById("demo-chat-confirm");
  const approveBtns = document.querySelectorAll("[data-demo-approve]");
  const confirmApproval = () => {
    larkCard?.classList.add("confirmed");
    approveBtns.forEach((b) => { b.disabled = true; });
    if (chatConfirm) chatConfirm.hidden = false;
  };
  approveBtns.forEach((b) => b.addEventListener("click", confirmApproval));

  // Interactive Inbox -> Todo badge sync demo
  let inboxNeed = 3;
  let todoOpen = 2;
  const syncBadges = () => {
    const ib = document.getElementById("demo-inbox-badge");
    const tb = document.getElementById("demo-todo-badge");
    if (ib) { ib.textContent = inboxNeed; ib.classList.toggle("zero", inboxNeed <= 0); }
    if (tb) { tb.textContent = todoOpen; tb.classList.toggle("zero", todoOpen <= 0); }
  };
  document.querySelectorAll("[data-demo-ack]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const card = btn.closest(".item-card");
      if (!card || card.dataset.done) return;
      card.dataset.done = "1";
      inboxNeed = Math.max(0, inboxNeed - 1);
      if (btn.dataset.demoAck === "promote") {
        todoOpen += 1;
        btn.textContent = "✓ 已转入待办";
        btn.classList.add("ok");
      } else {
        btn.textContent = "✓ 已看";
        btn.classList.add("ok");
      }
      card.style.opacity = "0.58";
      syncBadges();
    });
  });

  // Quick start code tabs & copy
  const qsTabs = document.querySelectorAll("[data-qs-tab]");
  const qsPres = document.querySelectorAll("[data-qs-pre]");
  let activeQs = "build";
  qsTabs.forEach((b) => b.addEventListener("click", () => {
    activeQs = b.dataset.qsTab;
    qsTabs.forEach((x) => x.classList.toggle("on", x.dataset.qsTab === activeQs));
    qsPres.forEach((p) => p.classList.toggle("on", p.dataset.qsPre === activeQs));
  }));
  const copyBtn = document.getElementById("qs-copy");
  copyBtn?.addEventListener("click", async () => {
    const pre = document.querySelector(`[data-qs-pre="${activeQs}"]`);
    if (!pre) return;
    try {
      await navigator.clipboard.writeText(pre.innerText.trim());
      const prev = copyBtn.textContent;
      copyBtn.textContent = dict[lang]["qs.copied"];
      setTimeout(() => { copyBtn.textContent = dict[lang]["qs.copy"] || prev; }, 1600);
    } catch {}
  });
})();
