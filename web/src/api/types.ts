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
  counts: {
    active_conversations: number;
    active_memories: number;
    active_topics: number;
    running_workers: number;
    worker_count: number;
  };
  runtime: RuntimeInfo;
  mood: {
    energy: number;
    sociability: number;
    last_reason: string;
    updated_at: string;
  } | null;
  emotions: Array<{
    id: number;
    kind: string;
    fact: string;
    reason: string;
    strength: number;
    strength_band: string;
    occurred_at: string;
    updated_at: string;
    version: number;
  }>;
  emotions_error: boolean;
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

export interface ConversationCompactResult {
  action: "compacted" | "prompt";
  messages: number;
}

export interface MemoryItem {
  version: number;
  id: number;
  subject_type: string;
  subject_target: string;
  memory_type: string;
  label: string;
  content: string;
  pinned: boolean;
  status: string;
  stability_days: number;
  recall_anchor_at: string;
  forget_at?: string;
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
  stability_days: number;
  recall_anchor_at: string;
  forget_at: string;
  forgotten_at?: string;
  source_total: number;
  source_available: number;
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
  rules_chars: number;
  qq_chars: number;
  worker_chars: number;
  life_chars: number;
  updated_at: string;
}

export interface PersonaTexts {
  persona_card: string;
  common_rules: string;
  qq_prompt: string;
  life_prompt: string;
  worker_prompt: string;
}

export interface PersonaDetail extends PersonaSummary, PersonaTexts {}

export interface PersonasData {
  items: PersonaSummary[];
  active_id: number;
}

export type PersonaTemplate = PersonaTexts;
export type PersonaSessionMode = "group" | "private" | "life" | "worker";

export interface IdentityData {
  name: string;
  master_user_id: number;
  master_name: string;
  master_available: boolean;
  version: number;
}

export interface IdentityFriend {
  user_id: number;
  name: string;
  active: boolean;
  blocked: boolean;
}

export interface IdentityFriendsData {
  items: IdentityFriend[];
}

export interface IdentityInput {
  name: string;
  master_user_id: number;
  version: number;
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
  persona: PersonaTexts & {
    preset_id: number;
    preset_name: string;
  };
  agent: {
    think_interval: number;
    think_debounce_ms: number;
    max_step: number;
    life_max_step: number;
    life_daily_request_limit: number;
    worker_max_step: number;
    max_group_concurrency: number;
    max_private_concurrency: number;
    worker_count: number;
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
  retention: {
    initial_half_life_days: number;
    max_half_life_days: number;
    reinforce_factor: number;
    forget_threshold: number;
    history_days: number;
  };
  media: {
    max_bytes_mb: number;
    image_quality: number;
    narrow_edge: number;
  };
  debug: {
    log_agent_content: boolean;
  };
}

export type SettingsSectionKey =
  | "agent"
  | "chat"
  | "compactor"
  | "maintenance"
  | "retention"
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
  reasoning_effort: string;
  api_key_hint: string;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface ModelSelection {
  qq: number;
  life: number;
  worker: number;
  version: number;
}

export interface ModelsData {
  selection: ModelSelection;
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
    rules_chars: number;
    qq_chars: number;
    life_chars: number;
    worker_chars: number;
  };
  context: {
    enabled_group_count: number;
    enabled_group_ids: number[];
    think_interval_sec: number;
    think_debounce_ms: number;
    max_step: number;
    life_max_step: number;
    worker_max_step: number;
    compactor: RuntimeConfig["compactor"];
  };
  model: {
    profiles: Record<string, { name: string; model_name: string; reasoning_effort: string; base_url: string; context_window: number }>;
    mcp_tool_count: number;
    mcp_statuses?: { name: string; enabled: boolean; connected: boolean; tool_count: number; error?: string }[];
  };
  runtime: {
    connected: boolean;
    agent_ready: boolean;
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

export interface LifeState {
  awake: boolean;
  activity: string;
  activity_since?: string;
  sleep_started_at?: string;
  woke_at?: string;
  last_error: string;
  last_error_code: string;
  running: boolean;
  updated_at: string;
  retry_count: number;
  retry_at?: string;
  retry_exhausted: boolean;
}

export interface LifeSchedule {
  id: number;
  at?: string;
  cron?: string;
  next_run_at: string;
  text: string;
  enabled: boolean;
  purpose: string;
  created_at: string;
  updated_at: string;
}

export interface DailyUsage {
  date: string;
  limit: number;
  total: number;
  life: number;
  worker: number;
  failures: number;
  status: "disabled" | "normal" | "near" | "over";
}

export interface LifeData {
  daily_usage: DailyUsage | null;
  daily_usage_unavailable: boolean;
  state: LifeState;
  schedules: LifeSchedule[];
  workers: LifeWorker[];
}

export interface LifeWorker {
  id: number;
  title: string;
  status: string;
  active: boolean;
  last_error: string;
  updated_at: string;
}

export interface WorkerHistoryEntry {
  id: number;
  at: string;
  role: string;
  text: string;
  tool_name?: string;
  tool_status?: string;
  tool_calls?: { name: string; arguments: string }[];
  arguments?: string;
}

export interface WorkerHistoryData {
  entries: WorkerHistoryEntry[];
  running_tools: WorkerHistoryEntry[];
  pending_entries: WorkerHistoryEntry[];
  has_more: boolean;
  next_before_id?: number;
}

export interface WorkerEntryData {
  text: string;
  total: number;
  complete: boolean;
  next_offset?: number;
  arguments?: string;
  tool_calls?: { name: string; arguments: string }[];
}

export interface DiaryEntry {
  date: string;
  markdown: string;
  created_at: string;
  updated_at: string;
}

export interface DiaryDayData {
  entry: DiaryEntry | null;
  previous_date: string | null;
  next_date: string | null;
}

export interface DiaryMonthData {
  month: string;
  dates: string[];
}

export interface LifeScheduleInput {
  text: string;
  at?: string;
  after_minutes?: number;
  cron?: string;
  enabled?: boolean;
}

export interface LifeScheduleUpdate {
  text?: string;
  enabled?: boolean;
  at?: string;
  after_minutes?: number;
  cron?: string;
}
export interface LogField {
  name: string;
  value: string;
  formatted_value?: string;
}
