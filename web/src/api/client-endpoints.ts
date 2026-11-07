import { apiGet, apiMutate, buildQuery } from "./client";
import type {
  ConversationsData,
  DashboardData,
  ListPayload,
  MCPServerDetail,
  MCPServersData,
  DiaryDayData,
  DiaryMonthData,
  LifeData,
  LifeScheduleInput,
  LifeScheduleUpdate,
  MemoryDetail,
  MemoryItem,
  Member,
  ModelProfile,
  ModelStatsData,
  ModelsData,
  PersonaDetail,
  PersonaTemplate,
  IdentityData,
  IdentityFriendsData,
  IdentityInput,
  PersonasData,
  SessionInfo,
  SettingsData,
  SettingsSectionKey,
  Sticker,
  SystemData,
  TopicDetailData,
  TopicConversationsData,
  TopicListItem,
} from "./types";

export const sessionApi = {
  get: () => apiGet<SessionInfo>("/api/v1/session"),
  login: (adminKey: string) =>
    apiMutate<void>("POST", "/api/v1/session", { admin_key: adminKey }),
  logout: () => apiMutate<void>("DELETE", "/api/v1/session"),
};

export const dashboardApi = {
  get: () => apiGet<DashboardData>("/api/v1/dashboard"),
};

interface ListQuery {
  keyword?: string;
  sort?: string;
  order?: string;
  page?: number;
  page_size?: number;
}

export const stickerApi = {
  list: (query: ListQuery) =>
    apiGet<ListPayload<Sticker>>(`/api/v1/stickers${buildQuery(query)}`),
  remove: (id: number) => apiMutate<void>("DELETE", `/api/v1/stickers/${id}`),
};

export const memberApi = {
  list: (query: ListQuery) =>
    apiGet<ListPayload<Member>>(`/api/v1/members${buildQuery(query)}`),
};

export interface ConversationUpdate {
  blocked?: boolean;
  extra_prompt?: string;
}

export const conversationApi = {
  list: () => apiGet<ConversationsData>("/api/v1/conversations"),
  update: (kind: string, targetId: number, body: ConversationUpdate) =>
    apiMutate<ConversationsData>(
      "PATCH",
      `/api/v1/conversations/${kind}/${targetId}`,
      body,
    ),
};

export interface MemoryQuery extends ListQuery {
  subject_type?: string;
  subject_target?: string;
  memory_type?: string;
  status?: string;
  user_id?: number;
}

export const memoryApi = {
  list: (query: MemoryQuery) =>
    apiGet<ListPayload<MemoryItem>>(`/api/v1/memories${buildQuery(query)}`),
  get: (id: number) => apiGet<MemoryDetail>(`/api/v1/memories/${id}`),
  updateStatus: (id: number, status: string) =>
    apiMutate<MemoryItem>("PATCH", `/api/v1/memories/${id}`, { status }),
};

export interface TopicQuery {
  conversation?: string;
  status?: string;
  keyword?: string;
  page?: number;
  page_size?: number;
  sort?: string;
  order?: string;
}

export const topicApi = {
  conversations: () =>
    apiGet<TopicConversationsData>("/api/v1/topics/conversations"),
  list: (query: TopicQuery) =>
    apiGet<ListPayload<TopicListItem>>(`/api/v1/topics${buildQuery(query)}`),
  get: (id: number) => apiGet<TopicDetailData>(`/api/v1/topics/${id}`),
};

export const personaApi = {
  list: () => apiGet<PersonasData>("/api/v1/personas"),
  get: (id: number) => apiGet<PersonaDetail>(`/api/v1/personas/${id}`),
  template: () => apiGet<PersonaTemplate>("/api/v1/personas/template"),
  create: (body: PersonaInput) =>
    apiMutate<PersonaDetail>("POST", "/api/v1/personas", body),
  update: (id: number, body: PersonaInput & { version: number }) =>
    apiMutate<PersonaDetail>("PATCH", `/api/v1/personas/${id}`, body),
  remove: (id: number) => apiMutate<void>("DELETE", `/api/v1/personas/${id}`),
  activate: (id: number) =>
    apiMutate<void>("POST", `/api/v1/personas/${id}/activate`),
  clone: (id: number) =>
    apiMutate<PersonaDetail>("POST", `/api/v1/personas/${id}/clone`),
};

export interface PersonaInput {
  name: string;
  description: string;
  persona_card: string;
  simulation_prompt: string;
  life_prompt: string;
}

export const identityApi = {
  get: () => apiGet<IdentityData>("/api/v1/identity"),
  friends: () => apiGet<IdentityFriendsData>("/api/v1/identity/friends"),
  update: (body: IdentityInput & { master_name?: string }) =>
    apiMutate<IdentityData>("PATCH", "/api/v1/identity", body),
};

export const settingsApi = {
  get: () => apiGet<SettingsData>("/api/v1/settings"),
  save: (section: SettingsSectionKey, values: unknown, version: number) =>
    apiMutate<SettingsData>("PUT", `/api/v1/settings/${section}`, {
      version,
      values,
    }),
  reset: (section: SettingsSectionKey, version: number) =>
    apiMutate<SettingsData>("POST", `/api/v1/settings/${section}/reset`, {
      version,
    }),
};

export interface ModelInput {
  name: string;
  base_url: string;
  model_name: string;
  context_window: number;
  thinking_type: string;
  reasoning_effort: string;
  api_key?: string;
}

export const modelApi = {
  list: () => apiGet<ModelsData>("/api/v1/models"),
  create: (body: ModelInput) =>
    apiMutate<ModelProfile>("POST", "/api/v1/models", body),
  update: (id: number, body: ModelInput & { version: number }) =>
    apiMutate<ModelProfile>("PATCH", `/api/v1/models/${id}`, body),
  remove: (id: number, version: number) =>
    apiMutate<void>("DELETE", `/api/v1/models/${id}?version=${version}`),
  activate: (id: number) =>
    apiMutate<void>("POST", `/api/v1/models/${id}/activate`),
  test: (id: number) => apiMutate<void>("POST", `/api/v1/models/${id}/test`),
};

export interface MCPServerInput {
  name: string;
  enabled: boolean;
  type: string;
  url: string;
  command: string;
  args: string[];
  env: string[];
  headers: Record<string, string>;
  tool_names: string[];
}

export const mcpApi = {
  list: () => apiGet<MCPServersData>("/api/v1/mcp-servers"),
  get: (id: number) => apiGet<MCPServerDetail>(`/api/v1/mcp-servers/${id}`),
  create: (body: MCPServerInput) =>
    apiMutate<MCPServerDetail>("POST", "/api/v1/mcp-servers", body),
  update: (id: number, body: MCPServerInput & { version: number }) =>
    apiMutate<MCPServerDetail>("PATCH", `/api/v1/mcp-servers/${id}`, body),
  remove: (id: number, version: number) =>
    apiMutate<{ deleted: boolean }>(
      "DELETE",
      `/api/v1/mcp-servers/${id}?version=${version}`,
    ),
};

export const systemApi = {
  get: () => apiGet<SystemData>("/api/v1/system"),
  stats: (range: string) =>
    apiGet<ModelStatsData>(
      `/api/v1/system/model-stats${buildQuery({ range })}`,
    ),
  logDownloadUrl: (keyword: string, level: string) =>
    `/api/v1/system/logs/download${buildQuery({ keyword, level })}`,
};

export const diariesApi = {
  month: (month: string) =>
    apiGet<DiaryMonthData>(
      `/api/v1/diaries?month=${encodeURIComponent(month)}`,
    ),
  day: (date: string) =>
    apiGet<DiaryDayData>(`/api/v1/diaries/${encodeURIComponent(date)}`),
};

export const lifeApi = {
  get: () => apiGet<LifeData>("/api/v1/life"),
  createSchedule: (input: LifeScheduleInput) =>
    apiMutate<LifeData>("POST", "/api/v1/life/schedules", input),
  updateSchedule: (id: number, input: LifeScheduleUpdate) =>
    apiMutate<LifeData>("PATCH", `/api/v1/life/schedules/${id}`, input),
  wake: () => apiMutate<LifeData>("POST", "/api/v1/life/wake"),
  run: (note?: string) =>
    apiMutate<LifeData>("POST", "/api/v1/life/run", note ? { note } : {}),
};
