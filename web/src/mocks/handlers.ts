import { http, HttpResponse, delay } from "msw";
import type {
  Conversation,
  MemoryItem,
  ModelProfile,
  MCPServerDetail,
  PersonaDetail,
  Sticker,
  TopicDetailData,
  TopicListItem,
  TopicSummary,
  TopicSummaryChange,
} from "@/api/types";

const emptySeed = import.meta.env.VITE_MOCK_SEED === "empty";

function ok(data: unknown, warning?: { code: string }) {
  return HttpResponse.json(warning ? { data, warning } : { data });
}

function fail(status: number, code: string, message: string) {
  return HttpResponse.json({ error: { code, message } }, { status });
}

function page<T>(items: T[], request: Request) {
  const url = new URL(request.url);
  const pageIndex = Math.max(1, Number(url.searchParams.get("page") || "1"));
  const size = Math.max(1, Number(url.searchParams.get("page_size") || "15"));
  const start = (pageIndex - 1) * size;
  return {
    items: items.slice(start, start + size),
    total: items.length,
    page: pageIndex,
    page_size: size,
  };
}

const now = Date.now();
const iso = (offsetMinutes: number) =>
  new Date(now - offsetMinutes * 60_000).toISOString();

const stickers: Sticker[] = emptySeed
  ? []
  : Array.from({ length: 18 }, (_, index) => ({
      id: index + 1,
      file_name: `sticker-${index + 1}.webp`,
      description: ["开心", "疑惑", "贴贴", "打滚", "晚安"][index % 5] + ` ${index + 1}`,
      use_count: (index * 7) % 30,
      updated_at: iso(index * 40),
      file_url: "",
    }));

const members = emptySeed
  ? []
  : Array.from({ length: 9 }, (_, index) => ({
      user_id: 10001 + index,
      nickname: ["小满", "阿澈", "白桃", "柚子", "星野", "眠眠", "橘猫", "雨声", "七海"][index],
      updated_at: iso(index * 120),
      names: [
        { value: `群昵称${index}`, group_id: 90001 + (index % 2) },
        { value: `别名${index}`, group_id: 90001 + (index % 2) },
        { value: `旧称呼${index}`, group_id: 90002 },
      ],
      knowledge_count: index * 2,
    }));

const conversations: Conversation[] = emptySeed
  ? []
  : [
      {
        kind: "group",
        target_id: 90001,
        name: "摸鱼小群",
        remote_remark: "",
        blocked: false,
        extra_prompt: "这个群比较随性，可以多闲聊。",
        active: true,
        updated_at: iso(30),
      },
      {
        kind: "group",
        target_id: 90002,
        name: "测试群",
        remote_remark: "",
        blocked: true,
        extra_prompt: "",
        active: true,
        updated_at: iso(120),
      },
      {
        kind: "private",
        target_id: 10001,
        name: "小满",
        remote_remark: "",
        blocked: false,
        extra_prompt: "",
        active: true,
        updated_at: iso(60),
      },
      {
        kind: "private",
        target_id: 10002,
        name: "阿澈",
        remote_remark: "",
        blocked: false,
        extra_prompt: "",
        active: false,
        updated_at: iso(2000),
      },
    ];

const memories: MemoryItem[] = emptySeed
  ? []
  : Array.from({ length: 16 }, (_, index) => ({
      id: index + 1,
      subject_type: ["group", "user", "self", "object"][index % 4],
      subject_target: ["90001", "10001", "self", "commute"][index % 4],
      memory_type: index % 2 === 0 ? "profile" : "experience",
      label: index % 3 === 0 ? "关于通勤的偏好" : "",
      content:
        index % 3 === 0
          ? "她记得对方每周一早上会坐很久的地铁，喜欢靠窗的位置。"
          : `这是第 ${index + 1} 条记忆内容，用来检查列表截断、筛选和详情展示。`,
      pinned: index % 7 === 0,
      status: index % 9 === 0 ? "archived" : "active",
      created_at: iso(index * 600),
      updated_at: iso(index * 300),
    }));

const summary = (index: number): TopicSummary => ({
  title: `话题 ${index + 1}：周末安排`,
  gist: "大家在讨论周末要不要一起看电影，最后约在周六下午。",
  participants: [
    { user_id: 10001, nickname: "小满" },
    { user_id: 10002, nickname: "阿澈" },
  ],
  open_loops: ["确认电影场次", "决定谁买票"],
  recent_turns: ["小满提议看新上映的动画", "阿澈说周六下午有空"],
  keywords: ["周末", "电影", "动画"],
});

const changes = (index: number): TopicSummaryChange[] => [
  {
    captured_at: iso(index * 200),
    initial: false,
    title: { changed: true, segments: [
      { kind: "equal", text: `话题 ${index + 1}：` },
      { kind: "remove", text: "周末安排" },
      { kind: "add", text: "周末看电影" },
    ] },
    gist: { changed: true, segments: [{ kind: "add", text: "定下了周六下午的时间。" }] },
    added_open_loops: ["确认电影场次"],
    removed_open_loops: [],
  },
  {
    captured_at: iso(index * 400),
    initial: true,
    title: { changed: true, segments: [{ kind: "add", text: `话题 ${index + 1}：周末安排` }] },
    gist: { changed: true, segments: [{ kind: "add", text: "大家在讨论周末要不要一起看电影。" }] },
    added_open_loops: ["决定谁买票"],
    removed_open_loops: [],
  },
];

const topics: TopicListItem[] = emptySeed
  ? []
  : Array.from({ length: 7 }, (_, index) => ({
      id: index + 1,
      title: summary(index).title,
      gist: summary(index).gist,
      keywords: summary(index).keywords,
      status: index % 2 === 0 ? "active" : "dormant",
      conversation_kind: index % 2 === 0 ? "group" : "private",
      target_id: index % 2 === 0 ? 90001 : 10001,
      message_count: 12 + index * 3,
      anchor_count: 2,
      source_count: 5,
      open_loop_count: 2,
      updated_at: iso(index * 90),
    }));

const topicDetails = new Map<number, TopicDetailData>(
  topics.map((topic, index) => [
    topic.id,
    {
      thread: topic,
      current_summary: summary(index),
      current_summary_version: 3,
      summary_snapshot_count: 4,
      summary_changes: changes(index),
      message_limit: 80,
      messages: Array.from({ length: 6 }, (_, messageIndex) => ({
        id: index * 100 + messageIndex + 1,
        sender: messageIndex % 2 === 0 ? "小满" : "阿澈",
        user_id: messageIndex % 2 === 0 ? 10001 : 10002,
        text:
          messageIndex === 4
            ? "这条消息已经被撤回，用来检查撤回样式。"
            : `第 ${messageIndex + 1} 条关联消息，按时间顺序排列。`,
        message_time: iso(index * 90 + messageIndex),
        recalled: messageIndex === 4,
        anchor: messageIndex === 1,
        source: messageIndex < 3,
      })),
    },
  ]),
);

const personas: PersonaDetail[] = emptySeed
  ? []
  : [
      {
        id: 1,
        name: "默认人格",
        description: "内置的默认预设",
        builtin: true,
        active: true,
        version: 1,
        card_chars: 65,
        sim_chars: 51,
        updated_at: iso(500),
        persona_card:
          "你是沐沐，一个住在群里的女孩。\n[群] 群里说话简短、轻快。\n[私] 私聊可以更亲近一些。\n## 说话风格\n喜欢用短句，偶尔撒娇。",
        simulation_prompt:
          "## 工具\n- speak：想发言时调用。\n- getCurrentStatus：需要当前情感时调用。",
      },
      {
        id: 2,
        name: "深夜电台",
        description: "适合深夜的安静语气",
        builtin: false,
        active: false,
        version: 3,
        card_chars: 60,
        sim_chars: 40,
        updated_at: iso(200),
        persona_card: "你是沐沐，深夜时说话更慢、更轻。",
        simulation_prompt: "## 工具\n保持安静，除非被点名。",
      },
    ];

const defaultConfig = {
  persona: {
    preset_id: 1,
    preset_name: "默认人格",
    persona_card: personas[0]?.persona_card ?? "",
    simulation_prompt: personas[0]?.simulation_prompt ?? "",
  },
  agent: {
    think_interval: 80,
    think_debounce_ms: 800,
    max_step: 20,
    max_group_concurrency: 2,
    max_private_concurrency: 2,
  },
  chat: {
    talk_frequency: 0.6,
    typing_simulation: true,
    typing_speed: 12,
    enable_time_rules: false,
    time_rules: [],
    rate_limit: { enabled: true, period_sec: 60, max_messages: 6, min_prob: 0.05 },
  },
  compactor: {
    trigger_ratio: 0.9,
    digest_budget_ratio: 0.3,
    recent_tail_ratio: 0.1,
    max_steps: 12,
    idle_minutes: 20,
    idle_min_ratio: 0.6,
  },
  maintenance: { memory_turns: 8, topic_turns: 6 },
  media: { max_bytes_mb: 10, image_quality: 82, narrow_edge: 720 },
  debug: { show_tool_calls: false },
};

const settings = {
  config: structuredClone(defaultConfig),
  versions: {
    agent: 2,
    chat: 2,
    compactor: 2,
    maintenance: 2,
    media: 2,
    debug: 2,
  } as Record<string, number>,
  agent_ready: true,
  secret_configured: true,
};

const models: ModelProfile[] = emptySeed
  ? []
  : [
      {
        id: 1,
        name: "主力模型",
        provider: "openai",
        base_url: "https://api.example.com/v1",
        model_name: "gpt-5",
        context_window: 400000,
        thinking_type: "enabled",
        reasoning_effort: "medium",
        api_key_hint: "sk-…9f2a",
        active: true,
        version: 4,
        created_at: iso(5000),
        updated_at: iso(120),
      },
      {
        id: 2,
        name: "备用模型",
        provider: "openai",
        base_url: "https://api.example.com/v1",
        model_name: "gpt-5-mini",
        context_window: 200000,
        thinking_type: "",
        reasoning_effort: "",
        api_key_hint: "sk-…1c3d",
        active: false,
        version: 2,
        created_at: iso(4000),
        updated_at: iso(400),
      },
    ];

const mcpServers: MCPServerDetail[] = emptySeed
  ? []
  : [
      {
        id: 1,
        name: "weather",
        enabled: true,
        transport: "stdio",
        version: 2,
        position: 0,
        updated_at: iso(300),
        type: "stdio",
        url: "",
        command: "npx",
        args: ["-y", "@modelcontextprotocol/server-weather"],
        env: ["WEATHER_API_KEY=demo"],
        headers: {},
        tool_names: [],
      },
      {
        id: 2,
        name: "search",
        enabled: false,
        transport: "sse",
        version: 1,
        position: 1,
        updated_at: iso(800),
        type: "sse",
        url: "https://mcp.example.com/sse",
        command: "",
        args: [],
        env: [],
        headers: { Authorization: "Bearer demo" },
        tool_names: ["search"],
      },
    ];

let authenticated = import.meta.env.VITE_MOCK_AUTH === "1";
let logSeq = 1;

const sampleLogs = () => {
  const lines = [];
  for (let index = 0; index < 40; index += 1) {
    const level = ["DEBUG", "INFO", "INFO", "WARN", "ERROR"][index % 5];
    lines.push({
      seq: logSeq++,
      time: new Date(now - (40 - index) * 1000).toLocaleTimeString("zh-CN", {
        hour12: false,
      }),
      level,
      message: `示例运行日志 ${index + 1}：处理消息与工具调用`,
      fields: [
        { name: "session", value: `group:${90001 + (index % 2)}` },
        { name: "step", value: String(index % 6) },
      ],
      text: "",
    });
  }
  return lines;
};

let liveLogs = sampleLogs();

export const handlers = [
  http.get("/api/v1/session", () =>
    ok({ enabled: true, authenticated, version: "mock-build" }),
  ),
  http.post("/api/v1/session", async ({ request }) => {
    const body = (await request.json()) as { admin_key?: string };
    if (!body.admin_key) return fail(401, "invalid_key", "密钥错误");
    authenticated = true;
    return new HttpResponse(null, { status: 204 });
  }),
  http.delete("/api/v1/session", () => {
    authenticated = false;
    return new HttpResponse(null, { status: 204 });
  }),

  http.get("/api/v1/dashboard", () =>
    ok({
      enabled_group_count: conversations.filter((item) => item.kind === "group" && item.active).length,
      counts: {
        memories: memories.length,
        members: members.length,
        active_memories: memories.filter((item) => item.status === "active").length,
        topics: topics.length,
        stickers: stickers.length,
      },
      runtime: { connected: true, self_id: 3889000000, mcp_tool_count: 4 },
      mood: {
        energy: 0.62,
        sociability: 0.71,
        last_reason: "刚聊完一个轻松的话题，心情不错。",
        updated_at: iso(5),
      },
      emotions: [
        { kind: "喜悦", strength: 0.7, reason: "被夸了", occurred_at: iso(5) },
        { kind: "好奇", strength: 0.5, reason: "对方提到了没听过的动画", occurred_at: iso(9) },
      ],
    }),
  ),

  http.get("/api/v1/stickers", async ({ request }) => {
    await delay(80);
    const url = new URL(request.url);
    const keyword = (url.searchParams.get("keyword") || "").toLowerCase();
    const filtered = stickers.filter((item) =>
      `${item.description} ${item.file_name}`.toLowerCase().includes(keyword),
    );
    return ok(page(filtered, request));
  }),
  http.delete("/api/v1/stickers/:id", ({ params }) => {
    const index = stickers.findIndex((item) => item.id === Number(params.id));
    if (index < 0) return fail(404, "not_found", "记录不存在");
    stickers.splice(index, 1);
    return new HttpResponse(null, { status: 204 });
  }),

  http.get("/api/v1/members", async ({ request }) => {
    await delay(80);
    return ok(page(members, request));
  }),

  http.get("/api/v1/conversations", () =>
    ok({
      groups: conversations.filter((item) => item.kind === "group"),
      friends: conversations.filter((item) => item.kind === "private"),
    }),
  ),
  http.patch("/api/v1/conversations/:kind/:id", async ({ request, params }) => {
    const body = (await request.json()) as {
      blocked?: boolean;
      extra_prompt?: string;
    };
    const target = conversations.find(
      (item) => item.kind === params.kind && item.target_id === Number(params.id),
    );
    if (!target) return fail(404, "not_found", "会话不存在");
    if (body.blocked !== undefined) target.blocked = body.blocked;
    if (body.extra_prompt !== undefined) target.extra_prompt = body.extra_prompt;
    target.updated_at = new Date().toISOString();
    return ok({
      groups: conversations.filter((item) => item.kind === "group"),
      friends: conversations.filter((item) => item.kind === "private"),
    });
  }),

  http.get("/api/v1/memories", async ({ request }) => {
    await delay(80);
    const url = new URL(request.url);
    const keyword = (url.searchParams.get("keyword") || "").toLowerCase();
    const status = url.searchParams.get("status") || "";
    const subjectType = url.searchParams.get("subject_type") || "";
    const filtered = memories.filter(
      (item) =>
        (!keyword || `${item.label} ${item.content}`.toLowerCase().includes(keyword)) &&
        (!status || item.status === status) &&
        (!subjectType || item.subject_type === subjectType),
    );
    return ok(page(filtered, request));
  }),
  http.get("/api/v1/memories/:id", ({ params }) => {
    const item = memories.find((entry) => entry.id === Number(params.id));
    if (!item) return fail(404, "not_found", "记录不存在");
    return ok({
      item,
      revisions: [
        {
          id: 1,
          item_id: item.id,
          version: 1,
          before_content: "",
          after_content: item.content,
          before_status: "",
          after_status: item.status,
          reason: "初次记录",
          created_at: item.created_at,
        },
      ],
    });
  }),
  http.patch("/api/v1/memories/:id", async ({ request, params }) => {
    const body = (await request.json()) as { status: string };
    const item = memories.find((entry) => entry.id === Number(params.id));
    if (!item) return fail(404, "not_found", "记录不存在");
    if (body.status === "archived") item.status = "archived";
    else if (body.status === "active") item.status = "active";
    else return fail(400, "validation_failed", "状态无效");
    return ok(item);
  }),

  http.get("/api/v1/topics/conversations", () => {
    const seen = new Map<string, { kind: string; target_id: number; name: string }>();
    for (const topic of topics) {
      const key = `${topic.conversation_kind}:${topic.target_id}`;
      if (seen.has(key)) continue;
      const conversation = conversations.find(
        (item) => item.kind === topic.conversation_kind && item.target_id === topic.target_id,
      );
      seen.set(key, {
        kind: topic.conversation_kind,
        target_id: topic.target_id,
        name: conversation?.name ?? "",
      });
    }
    return ok({ items: [...seen.values()] });
  }),
  http.get("/api/v1/topics", async ({ request }) => {
    await delay(80);
    return ok(page(topics, request));
  }),
  http.get("/api/v1/topics/:id", ({ params }) => {
    const detail = topicDetails.get(Number(params.id));
    if (!detail) return fail(404, "not_found", "话题不存在");
    return ok(detail);
  }),

  http.get("/api/v1/personas", () =>
    ok({
      items: personas.map(({ persona_card: _card, simulation_prompt: _sim, ...rest }) => rest),
      active_id: personas.find((item) => item.active)?.id ?? 0,
    }),
  ),
  http.get("/api/v1/personas/template", () =>
    ok({
      persona_card: personas[0]?.persona_card ?? "",
      simulation_prompt: personas[0]?.simulation_prompt ?? "",
    }),
  ),
  http.get("/api/v1/personas/:id", ({ params }) => {
    const persona = personas.find((item) => item.id === Number(params.id));
    return persona ? ok(persona) : fail(404, "not_found", "预设不存在");
  }),
  http.post("/api/v1/personas", async ({ request }) => {
    const body = (await request.json()) as {
      name: string;
      description: string;
      persona_card: string;
      simulation_prompt: string;
    };
    if (personas.some((item) => item.name === body.name.trim())) {
      return fail(400, "preset_duplicate", "已经有同名预设了");
    }
    const created: PersonaDetail = {
      id: Math.max(...personas.map((item) => item.id), 0) + 1,
      name: body.name,
      description: body.description,
      builtin: false,
      active: false,
      version: 1,
      card_chars: body.persona_card.length,
      sim_chars: body.simulation_prompt.length,
      updated_at: new Date().toISOString(),
      persona_card: body.persona_card,
      simulation_prompt: body.simulation_prompt,
    };
    personas.push(created);
    return ok(created);
  }),
  http.patch("/api/v1/personas/:id", async ({ request, params }) => {
    const body = (await request.json()) as {
      name: string;
      description: string;
      persona_card: string;
      simulation_prompt: string;
      version: number;
    };
    const persona = personas.find((item) => item.id === Number(params.id));
    if (!persona) return fail(404, "not_found", "预设不存在");
    if (persona.version !== body.version) return fail(409, "version_conflict", "内容已被修改");
    persona.name = body.name;
    persona.description = body.description;
    persona.persona_card = body.persona_card;
    persona.simulation_prompt = body.simulation_prompt;
    persona.card_chars = body.persona_card.length;
    persona.sim_chars = body.simulation_prompt.length;
    persona.version += 1;
    persona.updated_at = new Date().toISOString();
    return ok(persona);
  }),
  http.delete("/api/v1/personas/:id", ({ params }) => {
    const index = personas.findIndex((item) => item.id === Number(params.id));
    if (index < 0) return fail(404, "not_found", "预设不存在");
    if (personas[index].builtin) return fail(400, "preset_builtin", "内置预设不能删除");
    if (personas[index].active) return fail(400, "preset_active", "正在使用的预设不能删除");
    personas.splice(index, 1);
    return new HttpResponse(null, { status: 204 });
  }),
  http.post("/api/v1/personas/:id/activate", ({ params }) => {
    const persona = personas.find((item) => item.id === Number(params.id));
    if (!persona) return fail(404, "not_found", "预设不存在");
    personas.forEach((item) => {
      item.active = item.id === persona.id;
    });
    return ok({ ok: true });
  }),
  http.post("/api/v1/personas/:id/clone", ({ params }) => {
    const persona = personas.find((item) => item.id === Number(params.id));
    if (!persona) return fail(404, "not_found", "预设不存在");
    const created: PersonaDetail = {
      ...persona,
      id: Math.max(...personas.map((item) => item.id), 0) + 1,
      name: `${persona.name} 副本`,
      active: false,
      builtin: false,
      version: 1,
      updated_at: new Date().toISOString(),
    };
    personas.push(created);
    return ok(created);
  }),

  http.get("/api/v1/settings", () => ok(settings)),
  http.put("/api/v1/settings/:section", async ({ request, params }) => {
    const body = (await request.json()) as { version: number; values: any };
    const section = String(params.section);
    if ((settings.versions[section] ?? 0) !== body.version) {
      return fail(409, "version_conflict", "内容已被修改");
    }
    (settings.config as any)[section] = { ...(settings.config as any)[section], ...body.values };
    settings.versions[section] = (settings.versions[section] ?? 0) + 1;
    return ok(settings);
  }),
  http.post("/api/v1/settings/:section/reset", async ({ request, params }) => {
    const body = (await request.json()) as { version: number };
    const section = String(params.section);
    if ((settings.versions[section] ?? 0) !== body.version) {
      return fail(409, "version_conflict", "内容已被修改");
    }
    (settings.config as any)[section] = structuredClone((defaultConfig as any)[section]);
    settings.versions[section] = (settings.versions[section] ?? 0) + 1;
    return ok(settings);
  }),

  http.get("/api/v1/models", () => ok({ items: models, secret_configured: true })),
  http.post("/api/v1/models", async ({ request }) => {
    const body = (await request.json()) as any;
    const created: ModelProfile = {
      id: Math.max(...models.map((item) => item.id), 0) + 1,
      name: body.name,
      provider: "",
      base_url: body.base_url,
      model_name: body.model_name,
      context_window: body.context_window,
      thinking_type: body.thinking_type,
      reasoning_effort: body.reasoning_effort,
      api_key_hint: body.api_key ? "sk-…mock" : "",
      active: false,
      version: 1,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    models.push(created);
    return ok(created);
  }),
  http.patch("/api/v1/models/:id", async ({ request, params }) => {
    const body = (await request.json()) as any;
    const model = models.find((item) => item.id === Number(params.id));
    if (!model) return fail(404, "not_found", "模型不存在");
    if (model.version !== body.version) return fail(409, "version_conflict", "内容已被修改");
    Object.assign(model, {
      name: body.name,
      base_url: body.base_url,
      model_name: body.model_name,
      context_window: body.context_window,
      thinking_type: body.thinking_type,
      reasoning_effort: body.reasoning_effort,
      api_key_hint: body.api_key ? "sk-…mock" : model.api_key_hint,
      updated_at: new Date().toISOString(),
    });
    model.version += 1;
    return ok(model);
  }),
  http.delete("/api/v1/models/:id", ({ params }) => {
    const index = models.findIndex((item) => item.id === Number(params.id));
    if (index < 0) return fail(404, "not_found", "模型不存在");
    if (models[index].active) return fail(400, "model_active", "当前模型不能删除");
    models.splice(index, 1);
    return new HttpResponse(null, { status: 204 });
  }),
  http.post("/api/v1/models/:id/activate", ({ params }) => {
    const model = models.find((item) => item.id === Number(params.id));
    if (!model) return fail(404, "not_found", "模型不存在");
    models.forEach((item) => {
      item.active = item.id === model.id;
    });
    return ok({ ok: true });
  }),
  http.post("/api/v1/models/:id/test", () => ok({ ok: true })),

  http.get("/api/v1/mcp-servers", () =>
    ok({
      items: mcpServers.map(({ type: _type, url: _url, command: _command, args: _args, env: _env, headers: _headers, tool_names: _tools, ...rest }) => rest),
      secret_configured: true,
    }),
  ),
  http.get("/api/v1/mcp-servers/:id", ({ params }) => {
    const server = mcpServers.find((item) => item.id === Number(params.id));
    return server ? ok(server) : fail(404, "not_found", "服务器不存在");
  }),
  http.post("/api/v1/mcp-servers", async ({ request }) => {
    const body = (await request.json()) as any;
    const created: MCPServerDetail = {
      id: Math.max(...mcpServers.map((item) => item.id), 0) + 1,
      name: body.name,
      enabled: body.enabled,
      transport: body.type,
      version: 1,
      position: mcpServers.length,
      updated_at: new Date().toISOString(),
      type: body.type,
      url: body.url,
      command: body.command,
      args: body.args,
      env: body.env,
      headers: body.headers,
      tool_names: body.tool_names,
    };
    mcpServers.push(created);
    return ok(created);
  }),
  http.patch("/api/v1/mcp-servers/:id", async ({ request, params }) => {
    const body = (await request.json()) as any;
    const server = mcpServers.find((item) => item.id === Number(params.id));
    if (!server) return fail(404, "not_found", "服务器不存在");
    if (server.version !== body.version) return fail(409, "version_conflict", "内容已被修改");
    Object.assign(server, {
      name: body.name,
      enabled: body.enabled,
      type: body.type,
      transport: body.type,
      url: body.url,
      command: body.command,
      args: body.args,
      env: body.env,
      headers: body.headers,
      tool_names: body.tool_names,
      updated_at: new Date().toISOString(),
    });
    server.version += 1;
    return ok(server);
  }),
  http.delete("/api/v1/mcp-servers/:id", ({ params }) => {
    const index = mcpServers.findIndex((item) => item.id === Number(params.id));
    if (index < 0) return fail(404, "not_found", "服务器不存在");
    mcpServers.splice(index, 1);
    return new HttpResponse(null, { status: 204 });
  }),

  http.get("/api/v1/system", () =>
    ok({
      persona: {
        preset_name: settings.config.persona.preset_name,
        card_chars: settings.config.persona.persona_card.length,
        sim_chars: settings.config.persona.simulation_prompt.length,
      },
      context: {
        enabled_group_count: 2,
        enabled_group_ids: [90001, 90002],
        think_interval_sec: settings.config.agent.think_interval,
        think_debounce_ms: settings.config.agent.think_debounce_ms,
        max_step: settings.config.agent.max_step,
        compactor: settings.config.compactor,
      },
      model: {
        active: {
          name: "主力模型",
          base_url: "https://api.example.com/v1",
          model_name: "gpt-5",
          thinking_type: "enabled",
          reasoning_effort: "medium",
        },
        mcp_tool_count: 4,
        context_window: 400000,
      },
      runtime: {
        connected: true,
        reconnect_sec: 5,
        schema_version: 17,
        latest_schema_version: 17,
        self_id: 3889000000,
        media_max_bytes_mb: 10,
      },
    }),
  ),
  http.get("/api/v1/system/model-stats", ({ request }) => {
    const url = new URL(request.url);
    const range = url.searchParams.get("range") || "24h";
    const points = range === "7d" ? 7 * 6 : range === "30d" ? 30 : 24;
    const series = Array.from({ length: points }, (_, index) => ({
      bucket_start: new Date(now - (points - index) * 3600_000).toISOString(),
      request_count: 20 + ((index * 7) % 30),
      total_tokens: 8000 + ((index * 1300) % 9000),
      latency_ms_sum: (900 + ((index * 170) % 1200)) * (20 + ((index * 7) % 30)),
    }));
    const totalRequests = series.reduce((sum, row) => sum + row.request_count, 0);
    const totalTokens = series.reduce((sum, row) => sum + row.total_tokens, 0);
    const totalLatency = series.reduce((sum, row) => sum + row.latency_ms_sum, 0);
    const aggregate = (label: string, requests: number) => ({
      label,
      request_count: requests,
      failure_count: Math.round(requests * 0.02),
      average_latency_ms: totalLatency / totalRequests,
      total_tokens: Math.round(totalTokens * (requests / totalRequests)),
      prompt_tokens: Math.round(totalTokens * 0.6),
      cached_tokens: Math.round(totalTokens * 0.2),
      usage_reported_count: Math.round(requests * 0.95),
    });
    return ok({
      range,
      totals: aggregate("全部", totalRequests),
      by_task: [aggregate("react", Math.round(totalRequests * 0.8)), aggregate("compaction", Math.round(totalRequests * 0.2))],
      by_model: [aggregate("gpt-5", Math.round(totalRequests * 0.9)), aggregate("gpt-5-mini", Math.round(totalRequests * 0.1))],
      series,
    });
  }),
  http.get("/api/v1/system/logs/stream", () => {
    const encoder = new TextEncoder();
    let timer: ReturnType<typeof setInterval> | undefined;
    const stream = new ReadableStream({
      start(controller) {
        const send = () => {
          liveLogs = [
            ...liveLogs.slice(-299),
            {
              seq: logSeq++,
              time: new Date().toLocaleTimeString("zh-CN", { hour12: false }),
              level: ["DEBUG", "INFO", "WARN"][logSeq % 3],
              message: `实时日志 ${logSeq}：mock 服务每秒推送一条`,
              fields: [{ name: "source", value: "mock" }],
              text: "",
            },
          ];
          try {
            controller.enqueue(
              encoder.encode(
                `event: snapshot\ndata: ${JSON.stringify({
                  lines: liveLogs,
                  total: liveLogs.length,
                  filtered: liveLogs.length,
                })}\n\n`,
              ),
            );
          } catch {
            if (timer) clearInterval(timer);
          }
        };
        send();
        timer = setInterval(send, 1000);
      },
      cancel() {
        if (timer) clearInterval(timer);
      },
    });
    return new HttpResponse(stream, {
      headers: {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
      },
    });
  }),
  http.get("/api/v1/system/logs/download", () =>
    new HttpResponse(liveLogs.map((line) => line.message).join("\n"), {
      headers: { "Content-Type": "text/plain; charset=utf-8" },
    }),
  ),
];
