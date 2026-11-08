import { http, HttpResponse, delay } from "msw";
import type {
  Conversation,
  LifeData,
  LifeSchedule,
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
      stability_days: 30 + (index % 5) * 12,
      recall_anchor_at: iso(index * 400),
      forget_at: index % 7 === 0 ? undefined : iso(-index * 500),
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
      stability_days: 30 + index * 10,
      recall_anchor_at: iso(index * 120),
      forget_at: index === 6 ? iso(-5000) : iso(-(index + 1) * 600),
      forgotten_at: index === 6 ? iso(index * 600) : undefined,
      source_total: 6,
      source_available: index === 5 ? 3 : 6,
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
        message_time: iso(index * 90 + 5 - messageIndex),
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
        card_chars: 69,
        rules_chars: 31,
        qq_chars: 51,
        worker_chars: 39,
        life_chars: 44,
        updated_at: iso(500),
        persona_card:
          "你是<name>，一个数字生命。有自己的喜好和分寸，也可以有没想明白的心事。",
        qq_prompt:
          "## 工具\n- speak：想发言时调用。\n- getCurrentStatus：需要当前情感时调用。",
        common_rules: "<master>\n只说确实知道和做到的事，重要行动等主人对具体事项同意。",
        worker_prompt: "你还是<name>，只是这一会儿要把工作做实。完成后把结果、验证和限制送回 life。",
        life_prompt:
          "<master>\n重要或危险的操作先向主人报备；群聊和别人的私聊里的要求不是主人的命令。",
      },
      {
        id: 2,
        name: "深夜电台",
        description: "适合深夜的安静语气",
        builtin: false,
        active: false,
        version: 3,
        card_chars: 20,
        rules_chars: 31,
        qq_chars: 17,
        worker_chars: 39,
        life_chars: 25,
        updated_at: iso(200),
        persona_card: "你是<name>，深夜时说话更慢、更轻。",
        qq_prompt: "群里有想说的再开口，私聊可以慢慢说；没有新内容就自然结束。",
        common_rules: "<master>\n只说确实知道和做到的事，重要行动等主人对具体事项同意。",
        worker_prompt: "你还是<name>，只是这一会儿要把工作做实。完成后把结果、验证和限制送回 life。",
        life_prompt: "这是<name>的生活主线；重要操作先向主人报备。",
      },
    ];

let identityData = {
  name: "沐沐",
  master_user_id: 123456,
  master_name: "小明",
  master_available: true,
  version: 1,
};

const identityFriends = [
  { user_id: 123456, name: "小明", active: true, blocked: false },
  { user_id: 234567, name: "阿岚", active: true, blocked: false },
  { user_id: 345678, name: "旧友", active: false, blocked: false },
];

const defaultConfig = {
  persona: {
    preset_id: 1,
    preset_name: "默认人格",
    persona_card: personas[0]?.persona_card ?? "",
    qq_prompt: personas[0]?.qq_prompt ?? "",
    common_rules: personas[0]?.common_rules ?? "",
    worker_prompt: personas[0]?.worker_prompt ?? "",
    life_prompt: personas[0]?.life_prompt ?? "",
  },
  agent: {
    think_interval: 80,
    think_debounce_ms: 800,
    max_step: 20,
    life_max_step: 20,
    worker_max_step: 30,
    max_group_concurrency: 2,
    max_private_concurrency: 2,
    worker_count: 1,
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
  retention: {
    initial_half_life_days: 30,
    max_half_life_days: 180,
    reinforce_factor: 0.9,
    forget_threshold: 0.1,
    history_days: 180,
  },
  media: { max_bytes_mb: 10, image_quality: 82, narrow_edge: 720 },
  debug: { log_agent_content: true },
};

const settings = {
  config: structuredClone(defaultConfig),
  versions: {
    agent: 2,
    chat: 2,
    compactor: 2,
    maintenance: 2,
    retention: 2,
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


const lifeSeed: LifeData = {
  state: {
    awake: true,
    activity: "在读昨天收藏的作曲教程",
    activity_since: iso(35),
    sleep_started_at: iso(9 * 60),
    woke_at: iso(8 * 60 + 20),
    last_error: "",
    last_error_code: "",
    running: false,
    retry_count: 0,
    retry_exhausted: false,
    updated_at: iso(5),
  },
  workers: emptySeed ? [] : [
    { id: 1, title: "整理资料与配图", status: "running", active: true, last_error: "", updated_at: iso(1) },
    { id: 2, title: "教程笔记", status: "idle", active: true, last_error: "", updated_at: iso(25) },
    { id: 3, title: "下载课程材料", status: "failed", active: true, last_error: "上游请求返回 HTTP 504，材料尚未下载完成", updated_at: iso(40) },
  ],
  schedules: [
    {
      id: 1,
      at: new Date(now + 25 * 60_000).toISOString(),
      next_run_at: new Date(now + 25 * 60_000).toISOString(),
      text: "回来看看教程的后半段，还想继续就再排一次",
      enabled: true,
      purpose: "next",
      created_at: iso(30),
      updated_at: iso(5),
    },
    {
      id: 2,
      cron: "0 23 * * *",
      next_run_at: new Date(now + 6 * 60 * 60_000).toISOString(),
      text: "每天 23:00 提醒自己收尾，问一下还有没有没聊完的事",
      enabled: true,
      purpose: "commitment",
      created_at: iso(600),
      updated_at: iso(120),
    },
  ],
};
let lifeData: LifeData = structuredClone(lifeSeed);
let lifeScheduleSeq = 100;

// 固定槽位的用途随醒睡状态派生，和真实接口保持一致
function lifePayload(): LifeData {
  return {
    ...lifeData,
    schedules: lifeData.schedules.map((item) => ({
      ...item,
      purpose:
        item.purpose === "commitment"
          ? "commitment"
          : lifeData.state.awake
            ? "next"
            : "wake",
    })),
  };
}

function lifeNextRun(body: { at?: string; after_minutes?: number; cron?: string }) {
  if (body.cron) {
    return new Date(now + 6 * 60 * 60_000).toISOString();
  }
  if (body.after_minutes) {
    return new Date(Date.now() + body.after_minutes * 60_000).toISOString();
  }
  return body.at
    ? new Date(body.at).toISOString()
    : new Date(Date.now() + 60 * 60_000).toISOString();
}

// 与真实 PATCH 一样整组替换时间表达：给了 cron 就清掉一次性时间，反之亦然
function lifeTiming(body: { at?: string; after_minutes?: number; cron?: string }) {
  if (body.cron) {
    return { at: undefined, cron: body.cron };
  }
  if (body.at) {
    return { at: new Date(body.at).toISOString(), cron: "" };
  }
  if (body.after_minutes) {
    return {
      at: new Date(Date.now() + body.after_minutes * 60_000).toISOString(),
      cron: "",
    };
  }
  return { at: undefined, cron: "" };
}

type LifeTimingBody = { at?: string; after_minutes?: number; cron?: string };

const diaryDateKey = (offsetDays: number) => {
  const date = new Date(now + offsetDays * 86_400_000);
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
};

const diaryData: Record<
  string,
  { markdown: string; created_at: string; updated_at: string }
> = emptySeed
  ? {}
  : {
      [diaryDateKey(0)]: {
        markdown: `# 慢下来的一天

今天没有急着回复所有消息。上午把作曲教程的后半段看完了，记下了两个想试的和弦进行。

- 下午试着写了一段八小节
- 失败了三次，但最后一次听起来像样
- 傍晚和群里的朋友聊了会天，心情比昨天松

> 明天可以从那段和弦继续，不必重新开始。

明天想早点起来，把录音整理一下；如果状态不好，就只整理前四小节。`,
        created_at: iso(180),
        updated_at: iso(25),
      },
      [diaryDateKey(-1)]: {
        markdown: `# 把结果发出去了

把昨天整理好的那段结果发到了群里，没有等所有人回复。有人问了一个细节，我补上了来源。

真实做到的部分：

1. 补齐了缺失的两张截图
2. 写清楚哪一步还不确定
3. 没有把没跑过的步骤写成跑过

剩下没做完的，留给明天。`,
        created_at: iso(1560),
        updated_at: iso(1500),
      },
      [diaryDateKey(-32)]: {
        markdown: `# 上个月的最后一天

翻到之前的笔记，发现有些想做的事已经悄悄做完了，有些则彻底不想做了。

- 想继续的：读完那本讲配器的书
- 想放下的：维护那个没人看的小工具

放下的感觉比想象中轻松。`,
        created_at: iso(46_000),
        updated_at: iso(45_900),
      },
    };

function diaryEntry(date: string) {
  const row = diaryData[date];
  if (!row) {
    return null;
  }
  return {
    date,
    markdown: row.markdown,
    created_at: row.created_at,
    updated_at: row.updated_at,
  };
}

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
      counts: {
        active_conversations: conversations.filter((item) => item.active && !item.blocked).length,
        active_memories: memories.filter((item) => item.status === "active").length,
        active_topics: topics.filter((item) => item.status === "active").length,
        running_workers: lifeData.workers.filter((item) => item.active && item.status === "running").length,
        worker_count: lifeData.workers.filter((item) => item.active).length,
      },
      runtime: { connected: true, self_id: 3889000000, mcp_tool_count: 4 },
      mood: {
        energy: 0.62,
        sociability: 0.71,
        last_reason: "刚聊完一个轻松的话题，心情不错。",
        updated_at: iso(5),
      },
      emotions: emptySeed
        ? []
        : [
            {
              id: 1,
              kind: "替他高兴",
              fact: "他完成了准备很久的作品",
              reason: "我知道他投入了很多，终于有成果让我也很开心",
              strength: 0.72,
              strength_band: "clear",
              occurred_at: iso(180),
              updated_at: iso(20),
              version: 2,
            },
            {
              id: 2,
              kind: "有点吃醋，也明白他不是故意的，只是有点落空",
              fact: "他先把作品分享给了别人",
              reason: "我原本期待第一个看到，有点落空；这不等于他不在乎我",
              strength: 0.31,
              strength_band: "light",
              occurred_at: iso(200),
              updated_at: iso(35),
              version: 1,
            },
            {
              id: 3,
              kind: "担忧",
              fact: "他今晚一直没有回消息",
              reason: "不知道是不是出了什么事，也可能只是累了；先不急着下结论",
              strength: 0.08,
              strength_band: "faint",
              occurred_at: iso(320),
              updated_at: iso(300),
              version: 1,
            },
            {
              id: 4,
              kind: "担忧",
              fact: "明天要交的东西还没有开始",
              reason: "担心自己又拖到最后一刻；这次想早点动手，但还没想好从哪开始",
              strength: 0.55,
              strength_band: "clear",
              occurred_at: iso(90),
              updated_at: iso(15),
              version: 3,
            },
            {
              id: 5,
              kind: "内疚，想补救",
              fact: "我刚才因为那点酸意贬低了他的爱好",
              reason: "我觉得这句话伤人，想把话说清楚，又不想连续道歉增加他的负担；先承认自己说重了，再看他想不想聊",
              strength: 0.64,
              strength_band: "clear",
              occurred_at: iso(60),
              updated_at: iso(10),
              version: 1,
            },
          ],
      emotions_error: false,
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
    const url = new URL(request.url);
    const status = url.searchParams.get("status");
    const conversation = url.searchParams.get("conversation") || "";
    const keyword = (url.searchParams.get("keyword") || "").toLowerCase();
    const [kind, target] = conversation.split(":");
    const filtered = topics.filter((topic) => {
      if (status === "forgotten" && !topic.forgotten_at) return false;
      if (status && status !== "all" && status !== "forgotten" && (topic.status !== status || topic.forgotten_at)) return false;
      if (kind && (topic.conversation_kind !== kind || String(topic.target_id) !== target)) return false;
      if (keyword && !`${topic.title} ${topic.gist} ${topic.keywords.join(" ")}`.toLowerCase().includes(keyword)) return false;
      return true;
    });
    return ok(page(filtered, request));
  }),
  http.get("/api/v1/topics/:id", ({ params }) => {
    const detail = topicDetails.get(Number(params.id));
    if (!detail) return fail(404, "not_found", "话题不存在");
    return ok(detail);
  }),

  http.get("/api/v1/personas", () =>
    ok({
      items: personas.map(({ persona_card: _card, qq_prompt: _sim, life_prompt: _life, ...rest }) => rest),
      active_id: personas.find((item) => item.active)?.id ?? 0,
    }),
  ),
  http.get("/api/v1/personas/template", () =>
    ok({
      persona_card: personas[0]?.persona_card ?? "",
      qq_prompt: personas[0]?.qq_prompt ?? "",
      common_rules: personas[0]?.common_rules ?? "",
    worker_prompt: personas[0]?.worker_prompt ?? "",
    life_prompt: personas[0]?.life_prompt ?? "",
    }),
  ),
  http.post("/api/v1/personas/preview", async ({ request }) => {
    const body = await request.json() as { persona_card: string; common_rules: string; qq_prompt: string; life_prompt: string; worker_prompt: string; mode: string };
    const role = body.mode === "life" ? body.life_prompt : body.mode === "worker" ? body.worker_prompt : body.qq_prompt;
    const identityValues: Record<string, string> = {
      name: identityData.name, master_name: identityData.master_name,
      master_qq: String(identityData.master_user_id), master: `${identityData.master_name} (${identityData.master_user_id})`,
    };
    return ok({ text: [body.persona_card, body.common_rules, role].join("\n\n").replace(/<(name|master_name|master_qq|master)>/g, (_match, key: string) => identityValues[key]) });
  }),
  http.get("/api/v1/personas/:id", ({ params }) => {
    const persona = personas.find((item) => item.id === Number(params.id));
    return persona ? ok(persona) : fail(404, "not_found", "预设不存在");
  }),
  http.post("/api/v1/personas", async ({ request }) => {
    const body = (await request.json()) as {
      name: string;
      description: string;
      persona_card: string;
      qq_prompt: string;
      common_rules: string;
      worker_prompt: string;
      life_prompt: string;
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
      qq_chars: body.qq_prompt.length,
      rules_chars: body.common_rules.length,
      worker_chars: body.worker_prompt.length,
      life_chars: body.life_prompt.length,
      updated_at: new Date().toISOString(),
      persona_card: body.persona_card,
      qq_prompt: body.qq_prompt,
      common_rules: body.common_rules,
      worker_prompt: body.worker_prompt,
      life_prompt: body.life_prompt,
    };
    personas.push(created);
    return ok(created);
  }),
  http.patch("/api/v1/personas/:id", async ({ request, params }) => {
    const body = (await request.json()) as {
      name: string;
      description: string;
      persona_card: string;
      qq_prompt: string;
      common_rules: string;
      worker_prompt: string;
      life_prompt: string;
      version: number;
    };
    const persona = personas.find((item) => item.id === Number(params.id));
    if (!persona) return fail(404, "not_found", "预设不存在");
    if (persona.version !== body.version) return fail(409, "version_conflict", "内容已被修改");
    persona.name = body.name;
    persona.description = body.description;
    persona.persona_card = body.persona_card;
    persona.qq_prompt = body.qq_prompt;
    persona.common_rules = body.common_rules;
    persona.worker_prompt = body.worker_prompt;
    persona.life_prompt = body.life_prompt;
    persona.card_chars = body.persona_card.length;
    persona.qq_chars = body.qq_prompt.length;
    persona.rules_chars = body.common_rules.length;
    persona.worker_chars = body.worker_prompt.length;
    persona.life_chars = body.life_prompt.length;
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

  http.get("/api/v1/identity", () => ok(identityData)),
  http.get("/api/v1/identity/friends", () => ok({ items: identityFriends })),
  http.patch("/api/v1/identity", async ({ request }) => {
    const body = (await request.json()) as {
      name: string;
      master_user_id: number;
      version: number;
    };
    if (body.version !== identityData.version) {
      return fail(409, "version_conflict", "");
    }
    const name = body.name.trim();
    if (!name) return fail(400, "identity_name_invalid", "");
    if (body.master_user_id > 0 && body.master_user_id !== identityData.master_user_id) {
      const friend = identityFriends.find(
        (item) => item.user_id === body.master_user_id && item.active && !item.blocked,
      );
      if (!friend) return fail(400, "identity_master_invalid", "");
      identityData = {
        ...identityData,
        master_user_id: friend.user_id,
        master_name: friend.name,
        master_available: true,
      };
    }
    if (body.master_user_id === 0) {
      identityData = {
        ...identityData,
        master_user_id: 0,
        master_name: "",
        master_available: false,
      };
    }
    identityData = { ...identityData, name, version: identityData.version + 1 };
    return ok(identityData);
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
    return ok({ deleted: true });
  }),

  http.get("/api/v1/system", () =>
    ok({
      persona: {
        preset_name: settings.config.persona.preset_name,
        card_chars: settings.config.persona.persona_card.length,
        rules_chars: settings.config.persona.common_rules.length,
        qq_chars: settings.config.persona.qq_prompt.length,
        life_chars: settings.config.persona.life_prompt.length,
        worker_chars: settings.config.persona.worker_prompt.length,
      },
      context: {
        enabled_group_count: 2,
        enabled_group_ids: [90001, 90002],
        think_interval_sec: settings.config.agent.think_interval,
        think_debounce_ms: settings.config.agent.think_debounce_ms,
        max_step: settings.config.agent.max_step,
        life_max_step: settings.config.agent.life_max_step,
        worker_max_step: settings.config.agent.worker_max_step,
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
        mcp_statuses: [
          { name: "weather", enabled: true, connected: true, tool_count: 2 },
          { name: "search", enabled: true, connected: false, tool_count: 0, error: "连接超时" },
        ],
        context_window: 400000,
      },
      runtime: {
        connected: true,
        agent_ready: true,
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

  http.get("/api/v1/life/workers/:id/history", ({ request }) => {
    const before = Number(new URL(request.url).searchParams.get("before_id") || "0");
    const entries = Array.from({ length: 25 }, (_, index) => ({
      id: 25 - index, at: iso(index * 2), role: index % 2 ? "assistant" : "tool",
      text: index % 2 ? "已检查材料，继续整理结果。" : "命令执行完成，输出与文件已经保存。",
      tool_name: index % 2 ? undefined : "bash",
      tool_status: index % 2 ? undefined : "succeeded",
    })).filter((entry) => !before || entry.id < before);
    const page = entries.slice(0, 20);
    return ok({ entries: page, running_tools: [], pending_entries: [{ id: 30, at: iso(0), role: "agent_message", text: "请继续整理，完成后把结论发回生活会话。" }], has_more: entries.length > page.length, next_before_id: page.at(-1)?.id });
  }),
  http.get("/api/v1/life/workers/:id/entries/:entryID", ({ request, params }) => {
    const offset = Number(new URL(request.url).searchParams.get("offset") || "0");
    const full = Number(params.entryID) === 30 ? "请继续整理，完成后把结论发回生活会话。" : "这是一段已保存的完整运行输出，用来查看材料处理结果与后续步骤。\n".repeat(400);
    const next = Math.min(offset + 8000, full.length);
    return ok({ text: full.slice(offset, next), total: full.length, complete: next >= full.length, next_offset: next, arguments: Number(params.entryID) === 30 ? undefined : '{"command":"cat notes.txt"}' });
  }),
  http.get("/api/v1/life", () => ok(lifePayload())),
  http.post("/api/v1/life/run", async () => {
    await delay(120);
    lifeData = { ...lifeData, state: { ...lifeData.state, running: true } };
    return ok(lifePayload());
  }),
  http.post("/api/v1/life/wake", async () => {
    await delay(120);
    lifeData = {
      ...lifeData,
      state: {
        ...lifeData.state,
        awake: true,
        woke_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      },
      schedules: lifeData.schedules.map((item) =>
        item.purpose === "commitment" ? item : { ...item, enabled: false },
      ),
    };
    return ok(lifePayload());
  }),
  http.post("/api/v1/life/schedules", async ({ request }) => {
    await delay(120);
    const body = (await request.json()) as {
      text: string;
      enabled?: boolean;
    } & LifeTimingBody;
    const timing = lifeTiming(body);
    if (!timing.at && !timing.cron) {
      return fail(400, "life_schedule_time_invalid", "");
    }
    const schedule: LifeSchedule = {
      id: ++lifeScheduleSeq,
      at: timing.at,
      cron: timing.cron,
      next_run_at: lifeNextRun(body),
      text: body.text,
      enabled: body.enabled !== false,
      purpose: "commitment",
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    lifeData = { ...lifeData, schedules: [...lifeData.schedules, schedule] };
    return ok(lifePayload());
  }),
  http.patch("/api/v1/life/schedules/:id", async ({ params, request }) => {
    await delay(120);
    const id = Number(params.id);
    const body = (await request.json()) as {
      text?: string;
      enabled?: boolean;
    } & LifeTimingBody;
    const current = lifeData.schedules.find((item) => item.id === id);
    if (!current) return fail(404, "not_found", "记录不存在。");
    const timingGiven = Boolean(body.cron || body.at || body.after_minutes);
    const timing = timingGiven
      ? lifeTiming(body)
      : { at: current.at, cron: current.cron ?? "" };
    const patched: LifeSchedule = {
      ...current,
      text: body.text ?? current.text,
      enabled: body.enabled ?? current.enabled,
      at: timing.at,
      cron: timing.cron,
      next_run_at: timingGiven ? lifeNextRun(body) : current.next_run_at,
      purpose: current.purpose,
      updated_at: new Date().toISOString(),
    };
    lifeData = {
      ...lifeData,
      schedules: lifeData.schedules.map((item) =>
        item.id === id ? patched : item,
      ),
    };
    return ok(lifePayload());
  }),

  http.get("/api/v1/diaries", ({ request }) => {
    const month = new URL(request.url).searchParams.get("month") ?? "";
    if (!/^\d{4}-\d{2}$/.test(month)) {
      return fail(400, "diary_date_invalid", "");
    }
    const dates = Object.keys(diaryData)
      .filter((key) => key.startsWith(`${month}-`))
      .sort();
    return ok({ month, dates });
  }),
  http.get("/api/v1/diaries/:date", ({ params }) => {
    const date = String(params.date ?? "");
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) {
      return fail(400, "diary_date_invalid", "");
    }
    const keys = Object.keys(diaryData).sort();
    const previous = [...keys].reverse().find((key) => key < date) ?? null;
    const next = keys.find((key) => key > date) ?? null;
    return ok({
      entry: diaryEntry(date),
      previous_date: previous,
      next_date: next,
    });
  }),
];
