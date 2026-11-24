(() => {
  const dict = {
    en: {
      "nav.mobile": "Mobile & Lark",
      "nav.arch": "Architecture",
      "nav.features": "Capabilities",
      "nav.start": "Quick Start",
      "hero.eyebrow": "Single Go Binary · Self-Hosted · MIT",
      "hero.h1a": "Carry all your multi-machine ",
      "hero.h1b": "AI Agents",
      "hero.h1c": " right in your pocket.",
      "hero.lede": "Swag links always-on servers, GPU nodes, and your laptop over outbound yamux tunnels into one Super Agent control deck—synced across desktop Web, Mobile PWA, and interactive Feishu cards with server-computed Inbox/Todo triage and a tiered model failover pool.",
      "hero.cta.gh": "View on GitHub",
      "hero.cta.start": "3-Min Self-Host",
      "mob.kicker": "Mobile & ChatOps Anywhere",
      "mob.h": "Step away from your desk without losing control of any agent.",
      "mob.p": "Leave heavy builds and coding sessions on your workstations—approve actions, reply with custom prompts, or inspect live terminals one-handed from your phone PWA or Feishu.",
      "b1.t": "QR-Paired Mobile PWA & Live Terminal Stream",
      "b1.p": "Run swag pair to generate a one-time QR code with a 1-year session cookie. Install to iOS/Android Home Screen for full-screen SSE chat, one-handed approval cards, and remote herdr terminal streaming.",
      "b2.t": "In-Place Feishu Card 2.0 + Custom Prompt Input",
      "b2.p": "Every approval card includes both one-tap option buttons and an inline text input form—reply with your own instructions directly inside Feishu while WebSockets update the card green in place.",
      "b3.t": "Context-Aware Strong-Model Todo Synthesis",
      "b3.p": "Read-only scheduled scans combine Tasks, Calendar, Approvals, IM mentions, and long-term memory using a strong reasoning tier to distill only what genuinely needs your action.",
      "b4.t": "Local-First & Private by Design",
      "b4.p": "No third-party cloud relay. SQLite WAL history, Markdown memory, and 0600 credential profiles stay inside your own ~/.swag/ directory.",
      "arch.kicker": "Interactive HTMLBox Topology",
      "arch.h": "Zero inbound ports on your machines. One authoritative Hub.",
      "arch.p": "Click or hover any HTMLBox node below to inspect protocols and data flow. Laptops and GPU servers dial outbound over yamux to join your fleet.",
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
      "f4.s": "Nightly Dream compacts long-term memory and mirrors it read-only across nodes. Approval cards support both quick buttons and custom free-text forms in place.",
      "qs.kicker": "Quick Start",
      "qs.h": "Single binary. Zero Node build step.",
      "qs.p": "Compile once on Go 1.24+, pair your phone PWA or compute nodes with one-time QR codes, and keep all data inside ~/.swag/.",
      "qs.copy": "Copy",
      "qs.copied": "Copied",
      "foot.copy": "MIT License · Built in Go & vanilla Web",
    },
    zh: {
      "nav.mobile": "手机 & 飞书",
      "nav.arch": "架构拓扑",
      "nav.features": "核心能力",
      "nav.start": "快速开始",
      "hero.eyebrow": "单二进制 Go 服务 · 自托管 · MIT 开源",
      "hero.h1a": "把多台电脑上的 ",
      "hero.h1b": "AI Agent",
      "hero.h1c": "，带在手机与飞书里。",
      "hero.lede": "Swag 用出站 yamux 隧道把常驻服务器、GPU 算力节点与个人 Mac 连成一套个人 Super Agent 调度底座——桌面 Web、手机 PWA 与飞书卡片实时同步，内置服务端统一收件箱/待办、模型反代池与夜间记忆做梦。",
      "hero.cta.gh": "前往 GitHub",
      "hero.cta.start": "3 分钟自托管",
      "mob.kicker": "随时随地 · 移动端与 ChatOps",
      "mob.h": "离开工位，依然单手掌控每一台机器上的 Agent。",
      "mob.p": "电脑继续留在桌上跑编译、训练与回归；手机扫码装好 PWA 或打开飞书聊天窗，审批、插话、看终端全都不需要回工位。",
      "b1.t": "扫码配对手机 PWA，单手审批与实时终端流",
      "b1.p": "在 Hub 运行 swag pair 生成 10 分钟一次性二维码，手机扫码即可登录并免密保持 1 年会话。支持添加到主屏幕全屏运行、断网重连 SSE、移动端触控优化及查看任意远程节点的实时终端输出。",
      "b2.t": "飞书卡片原位审批 + 自定义 Prompt 输入框",
      "b2.p": "不只有预设快捷按钮，每张需要确认的卡片下方都自带输入框——像 Codex 的 Other 一样直接在飞书里打字回复 Swag，WebSocket 回调原地把卡片翻绿，全程不跳浏览器。",
      "b3.t": "强模型上下文提炼，自动沉淀「飞书待办」",
      "b3.p": "定时只读扫描任务、日程、审批与 @我未读消息，结合历史记忆与进行中任务去重，用强模型精准筛选出真正需要你处理的事并写入待办中心。",
      "b4.t": "全链路自托管，数据与密钥永不离开你的机器",
      "b4.p": "没有第三方云中转、没有订阅绑架。SQLite WAL 对话库、Markdown 长期记忆与 0600 权限的凭证文件全部存在你自己的 ~/.swag/ 目录，配合尾网 / 内网安全直连。",
      "arch.kicker": "HTMLBox 交互式架构拓扑",
      "arch.h": "节点零入站端口，Hub 持有唯一权威状态。",
      "arch.p": "点击或悬停下方任意架构模块，查看通信协议与数据流向。你的笔记本和算力机只需主动出站连回 Hub，即可组成跨机 Super Agent 舰队。",
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
      "f4.s": "夜间 Dream 自动整理记忆并同步只读镜像到各节点；审批卡片通过飞书 card.action.trigger 支持一键确认与自定义 Prompt 表单回复，原地变绿回写。",
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
  const syncThemeBtn = () => { if (themeBtn) themeBtn.textContent = getTheme() === "dark" ? "☀" : "☾"; };
  themeBtn?.addEventListener("click", () => {
    document.documentElement.dataset.theme = getTheme() === "dark" ? "light" : "dark";
    syncThemeBtn();
  });
  syncThemeBtn();

  // Sticky topbar + Scroll Reveal
  const topbar = document.querySelector(".topbar");
  const onScroll = () => topbar?.classList.toggle("scrolled", window.scrollY > 12);
  window.addEventListener("scroll", onScroll, { passive: true });
  onScroll();

  if ("IntersectionObserver" in window) {
    const io = new IntersectionObserver((entries) => {
      entries.forEach((e) => { if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); } });
    }, { threshold: 0.12 });
    document.querySelectorAll(".reveal:not(.in)").forEach((el) => io.observe(el));
  } else {
    document.querySelectorAll(".reveal").forEach((el) => el.classList.add("in"));
  }

  // Interactive Control Deck tabs
  const navBtns = document.querySelectorAll("[data-deck-tab]");
  const views = document.querySelectorAll("[data-deck-view]");
  const titleEl = document.getElementById("dm-title");
  const subEl = document.getElementById("dm-sub");
  const metaMap = {
    chat: ["和 Swag 对话", "Router 在线 · 桌面与手机端实时同步"],
    inbox: ["收件箱", "服务端统一投影 · 需要你分诊的外部信号"],
    todos: ["待办中心", "待审批 · 进行中任务 · 强模型提炼的飞书待办"],
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

  // iPhone tabs (Feishu Card vs Mobile PWA)
  const phoneTabs = document.querySelectorAll("[data-phone-tab]");
  const phonePanes = document.querySelectorAll("[data-phone-pane]");
  phoneTabs.forEach((b) => b.addEventListener("click", () => {
    const t = b.dataset.phoneTab;
    phoneTabs.forEach((x) => x.classList.toggle("on", x.dataset.phoneTab === t));
    phonePanes.forEach((p) => p.classList.toggle("on", p.dataset.phonePane === t));
  }));

  // Interactive Feishu Card 2.0 (Quick Option + Custom Prompt Form) synced to Desktop Chat
  const larkCard = document.getElementById("lark-card");
  const larkDone = document.getElementById("demo-lark-done");
  const chatConfirm = document.getElementById("demo-chat-confirm");
  const approveBtns = document.querySelectorAll("[data-demo-approve]");
  const confirmReply = (replyText) => {
    larkCard?.classList.add("confirmed");
    approveBtns.forEach((b) => { b.disabled = true; });
    if (larkDone) larkDone.textContent = `✅ 已确认：「${replyText}」· 卡片原地更新`;
    if (chatConfirm) {
      chatConfirm.textContent = `✅ 已回复 Swag：「${replyText}」（飞书卡片原位同步）`;
      chatConfirm.hidden = false;
    }
  };
  approveBtns.forEach((b) => b.addEventListener("click", () => confirmReply(b.dataset.demoApprove || "批准发布 v0.7.3")));
  document.getElementById("demo-lark-form")?.addEventListener("submit", (e) => {
    e.preventDefault();
    const inp = document.getElementById("demo-lark-input");
    const val = (inp?.value || "").trim() || "先灰度 10% 观察 15 分钟再全量";
    confirmReply(val);
  });

  // Interactive Inbox -> Todo badge sync
  let inboxNeed = 2;
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
      } else {
        btn.textContent = "✓ 已看";
      }
      btn.classList.add("ok");
      card.style.opacity = "0.56";
      syncBadges();
    });
  });

  // HTMLBox Architecture Inspector
  const hbSpecs = {
    web: ["Desktop Web UI (Embedded)", "原生 HTML/CSS/JS 直接嵌入 Go 二进制，Mac 节点本地提供 127.0.0.1:17788 代理，零前端打包步骤。", "open http://127.0.0.1:17788"],
    mobile: ["Mobile PWA (iOS / Android)", "通过 swag pair 生成 10 分钟一次性二维码扫码登录，支持添加到主屏幕、断网自动重连 SSE 与单手审批卡片。", "swag pair"],
    lark: ["Feishu / Lark Bot (Card 2.0)", "通过 lark-cli event consume card.action.trigger 建立长连接，支持按钮审批与自定义输入表单，回调后原位把卡片更新为绿色。", "card.action.trigger · WS"],
    hub: ["Swag Hub (Single Go Binary)", "唯一权威 SQLite WAL 状态库、服务端收件箱/待办投影、/v1 多通道模型反代池、记忆管理与 yamux 节点控制面。", "swag --port 7788"],
    compute: ["Compute / GPU Worker Node", "主动出站连回 Hub（零入站端口），运行 herdr 持久终端、Codex / Claude Code、夜间 Dream 与强模型飞书上下文 Todo 分析。", "swag node --hub https://... --code ..."],
    laptop: ["Personal MacBook Node", "接管本地 Codex / Antigravity 桌面会话、同步只读记忆镜像 (~/.swag/memory-mirror/) 并提供本机 Web UI 入口。", "launchctl kickstart gui/$(id -u)/com.swag.node"],
  };
  const hboxes = document.querySelectorAll(".hbox[data-hb]");
  const insT = document.getElementById("hb-ins-title");
  const insD = document.getElementById("hb-ins-desc");
  const insC = document.getElementById("hb-ins-cmd");
  const selectHB = (k) => {
    const info = hbSpecs[k];
    if (!info) return;
    hboxes.forEach((b) => b.classList.toggle("active", b.dataset.hb === k));
    if (insT) insT.textContent = info[0];
    if (insD) insD.textContent = info[1];
    if (insC) insC.textContent = info[2];
  };
  hboxes.forEach((b) => {
    b.addEventListener("click", () => selectHB(b.dataset.hb));
    b.addEventListener("mouseenter", () => selectHB(b.dataset.hb));
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
      copyBtn.textContent = dict[lang]["qs.copied"];
      setTimeout(() => { copyBtn.textContent = dict[lang]["qs.copy"]; }, 1600);
    } catch {}
  });
})();
