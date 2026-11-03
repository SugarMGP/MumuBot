export interface SessionInfo {
  enabled: boolean;
  authenticated: boolean;
  version: string;
}

export interface RuntimeInfo {
  connected: boolean;
  self_id: number;
  mcp_tool_count: number;
}

export interface DashboardData {
  enabled_group_count: number;
  counts: {
    memories: number;
    members: number;
    active_memories: number;
    topics: number;
    stickers: number;
  };
  runtime: RuntimeInfo;
  mood: {
    energy: number;
    sociability: number;
    last_reason: string;
    updated_at: string;
  } | null;
  emotions: Array<{
    kind: string;
    strength: number;
    reason: string;
    occurred_at: string;
  }>;
}

export interface ListPayload<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface Sticker {
  id: number;
  file_name: string;
  description: string;
  use_count: number;
  updated_at: string;
  file_url: string;
}

export interface MemberName {
  value: string;
  group_id: number;
}

export interface Member {
  user_id: number;
  nickname: string;
  updated_at: string;
  names: MemberName[];
  knowledge_count: number;
}

export interface Conversation {
  kind: string;
  target_id: number;
  name: string;
  remote_remark: string;
  blocked: boolean;
  extra_prompt: string;
  active: boolean;
  updated_at: string;
}

export interface ConversationsData {
  groups: Conversation[];
  friends: Conversation[];
}

export interface MemoryItem {
  id: number;
  subject_type: string;
  subject_target: string;
  memory_type: string;
  label: string;
  content: string;
  pinned: boolean;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface MemoryRevision {
  id: number;
  item_id: number;
  version: number;
  before_content: string;
  after_content: string;
  before_status: string;
  after_status: string;
  reason: string;
  created_at: string;
}

export interface MemoryDetail {
  item: MemoryItem;
  revisions: MemoryRevision[];
}

export interface TopicSummary {
  title: string;
  gist: string;
  participants: Array<{ user_id: number; nickname: string }>;
  open_loops: string[];
  recent_turns: string[];
  keywords: string[];
}

export interface TopicDiffSegment {
  kind: "equal" | "add" | "remove";
  text: string;
}

export interface TopicTextDiff {
  changed: boolean;
  segments: TopicDiffSegment[];
}

export interface TopicSummaryChange {
  captured_at: string;
  initial: boolean;
  title: TopicTextDiff;
  gist: TopicTextDiff;
  added_open_loops: string[];
  removed_open_loops: string[];
}

export interface TopicMessage {
  id: number;
  sender: string;
  user_id: number;
  text: string;
  message_time: string;
  recalled: boolean;
  anchor: boolean;
  source: boolean;
}

export interface TopicListItem {
  id: number;
  title: string;
  gist: string;
  keywords: string[];
  status: string;
  conversation_kind: string;
  target_id: number;
  message_count: number;
  anchor_count: number;
  source_count: number;
  open_loop_count: number;
  updated_at: string;
}

export interface TopicConversationOption {
  kind: string;
  target_id: number;
  name: string;
}

export interface TopicConversationsData {
  items: TopicConversationOption[];
}

export interface TopicDetailData {
  thread: TopicListItem;
  current_summary: TopicSummary | null;
  current_summary_version: number;
  summary_snapshot_count: number;
  summary_changes: TopicSummaryChange[];
  messages: TopicMessage[];
  message_limit: number;
}

export interface PersonaSummary {
  id: number;
  name: string;
  description: string;
  builtin: boolean;
  active: boolean;
  version: number;
  card_chars: number;
  sim_chars: number;
  updated_at: string;
}

export interface PersonaDetail extends PersonaSummary {
  persona_card: string;
  simulation_prompt: string;
}

export interface PersonasData {
  items: PersonaSummary[];
  active_id: number;
}

export interface PersonaTemplate {
  persona_card: string;
  simulation_prompt: string;
}

export interface RateLimitConfig {
  enabled: boolean;
  period_sec: number;
  max_messages: number;
  min_prob: number;
}

export interface TimeRuleConfig {
  time_range: string;
  group_id: number;
  talk_value: number;
}

export interface RuntimeConfig {
  persona: {
    preset_id: number;
    preset_name: string;
    persona_card: string;
    simulation_prompt: string;
  };
  agent: {
    think_interval: number;
    think_debounce_ms: number;
    max_step: number;
    max_group_concurrency: number;
    max_private_concurrency: number;
  };
  chat: {
    talk_frequency: number;
    typing_simulation: boolean;
    typing_speed: number;
    enable_time_rules: boolean;
    time_rules: TimeRuleConfig[];
    rate_limit: RateLimitConfig;
  };
  compactor: {
    trigger_ratio: number;
    digest_budget_ratio: number;
    recent_tail_ratio: number;
    max_steps: number;
    idle_minutes: number;
    idle_min_ratio: number;
  };
  maintenance: {
    memory_turns: number;
    topic_turns: number;
  };
  media: {
    max_bytes_mb: number;
    image_quality: number;
    narrow_edge: number;
  };
  debug: {
    show_tool_calls: boolean;
  };
}

export type SettingsSectionKey =
  | "agent"
  | "chat"
  | "compactor"
  | "maintenance"
  | "media"
  | "debug";

export interface SettingsData {
  config: RuntimeConfig;
  versions: Record<string, number>;
  agent_ready: boolean;
  secret_configured: boolean;
}

export interface ModelProfile {
  id: number;
  name: string;
  provider: string;
  base_url: string;
  model_name: string;
  context_window: number;
  thinking_type: string;
  reasoning_effort: string;
  api_key_hint: string;
  active: boolean;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface ModelsData {
  items: ModelProfile[];
  secret_configured: boolean;
}

export interface MCPServerSummary {
  id: number;
  name: string;
  enabled: boolean;
  transport: string;
  version: number;
  position: number;
  updated_at: string;
}

export interface MCPServersData {
  items: MCPServerSummary[];
  secret_configured: boolean;
}

export interface MCPServerDetail extends MCPServerSummary {
  type: string;
  url: string;
  command: string;
  args: string[];
  env: string[];
  headers: Record<string, string>;
  tool_names: string[];
}

export interface SystemData {
  persona: {
    preset_name: string;
    card_chars: number;
    sim_chars: number;
  };
  context: {
    enabled_group_count: number;
    enabled_group_ids: number[];
    think_interval_sec: number;
    think_debounce_ms: number;
    max_step: number;
    compactor: RuntimeConfig["compactor"];
  };
  model: {
    active: {
      name: string;
      base_url: string;
      model_name: string;
      thinking_type: string;
      reasoning_effort: string;
    } | null;
    mcp_tool_count: number;
    context_window: number;
  };
  runtime: {
    connected: boolean;
    reconnect_sec: number;
    schema_version: number;
    latest_schema_version: number;
    self_id: number;
    media_max_bytes_mb: number;
  };
}

export interface AggregateRow {
  label: string;
  request_count: number;
  failure_count: number;
  average_latency_ms: number;
  total_tokens: number;
  prompt_tokens: number;
  cached_tokens: number;
  usage_reported_count: number;
}

export interface StatsSeriesRow {
  bucket_start: string;
  request_count: number;
  total_tokens: number;
  latency_ms_sum: number;
}

export interface ModelStatsData {
  range: string;
  totals: AggregateRow;
  by_task: AggregateRow[];
  by_model: AggregateRow[];
  series: StatsSeriesRow[];
}

export interface LogField {
  name: string;
  value: string;
}

export interface LogLine {
  seq: number;
  time: string;
  level: string;
  message: string;
  fields: LogField[];
  text: string;
}

export interface LogSnapshot {
  lines: LogLine[];
  total: number;
  filtered: number;
}

export interface WarningPayload {
  code: string;
}

export interface ApiErrorPayload {
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
  };
}
