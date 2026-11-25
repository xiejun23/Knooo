export type Preset = { id: string; name: string; baseUrl: string };

export const protocolLabels = {
 chat: 'Chat Completions',
 responses: 'Responses',
 anthropic: 'Anthropic Messages',
 gemini: 'Gemini API',
} as const;

export type APIProtocol = keyof typeof protocolLabels;
export type GatewayAPI = 'chat' | 'responses';

export type ReasoningEffort = '' | 'none' | 'low' | 'medium' | 'high';

export const reasoningOptions: { id: ReasoningEffort; label: string; badge: string }[] = [
 { id: '', label: '默认', badge: '' },
 { id: 'none', label: '关闭', badge: '关闭思考' },
 { id: 'low', label: '低', badge: '思考：低' },
 { id: 'medium', label: '中', badge: '思考：中' },
 { id: 'high', label: '高', badge: '思考：高' },
];

export function reasoningBadgeLabel(effort?: ReasoningEffort): string {
 return reasoningOptions.find(option => option.id === (effort ?? ''))?.badge ?? '';
}

export type Profile = {
 id: string;
 preset: string;
 name: string;
 baseUrl: string;
 model: string;
 ready: boolean;
 hasKey?: boolean;
 disabled?: boolean;
 reasoningEffort?: ReasoningEffort;
 inputBudget: number;
 outputBudget: number;
 icon?: string;
 gatewayApi?: GatewayAPI;
 apiProtocol?: APIProtocol;
};

export type GatewayExample = {
 name: string;
 baseUrl: string;
 api: GatewayAPI;
 icon: string;
};

export type Connections = {
 profiles: Profile[];
 presets: Preset[];
 gatewayExamples: GatewayExample[];
};

export type ProviderStatus = 'ready' | 'needs-model' | 'needs-key' | 'disabled';

export type QuickInputAction = {
 kind: 'url' | 'key';
 label: string;
 detail: string;
 profile: Profile;
 apiKey?: string;
};

export const relayPresets = new Set(['openrouter', 'siliconflow', 'compatible']);

export const presetIcons: Readonly<Partial<Record<string, string>>> = {
 openai: 'openai',
 anthropic: 'claude',
 gemini: 'gemini',
 deepseek: 'deepseek',
 dashscope: 'qwen',
 moonshot: 'kimi',
 minimax: 'minimax',
 zhipu: 'zhipu',
 openrouter: 'openrouter',
 siliconflow: 'siliconflow',
};

const presetKeywords: Readonly<Partial<Record<string, string>>> = {
 openai: 'openai gpt chatgpt',
 anthropic: 'anthropic claude sonnet opus',
 gemini: 'google gemini',
 deepseek: 'deepseek 深度求索',
 dashscope: 'qwen 通义 阿里 百炼 dashscope',
 moonshot: 'kimi moonshot 月之暗面',
 minimax: 'minimax minimaxi 海螺',
 zhipu: 'zhipu glm 智谱 bigmodel',
 openrouter: 'openrouter 聚合 中转',
 siliconflow: 'siliconflow 硅基流动 中转',
 compatible: 'custom openai compatible 自定义 中转',
 gateway: 'local gateway 本机 网关 ollama lmstudio magpie ccswitch',
};

export function emptyProfile(preset: Preset): Profile {
 return {
  id: '',
  preset: preset.id,
  name: preset.name,
  baseUrl: preset.baseUrl,
  model: '',
  ready: false,
  inputBudget: 24000,
  outputBudget: 4096,
 };
}

export function resolveIcon(profile: Pick<Profile, 'icon' | 'preset'>): string {
 return profile.icon || presetIcons[profile.preset] || 'generic';
}

export function resolveProtocol(profile: Pick<Profile, 'preset' | 'gatewayApi' | 'apiProtocol'>): APIProtocol {
 if (profile.preset === 'gateway') return profile.gatewayApi || 'chat';
 if (profile.apiProtocol) return profile.apiProtocol;
 if (profile.preset === 'anthropic' || profile.preset === 'gemini') return profile.preset;
 return 'chat';
}

export function providerCategoryLabel(profile: Pick<Profile, 'preset'>): string {
 if (profile.preset === 'gateway') return '本机服务';
 if (relayPresets.has(profile.preset)) return '中转聚合';
 return '官方直连';
}

export function providerStatus(profile: Profile): { kind: ProviderStatus; label: string } {
 if (profile.disabled) return { kind: 'disabled', label: '已停用' };
 if (profile.preset !== 'gateway' && !profile.hasKey && !profile.ready) {
  return { kind: 'needs-key', label: '待填密钥' };
 }
 if (!profile.model.trim()) return { kind: 'needs-model', label: '待设模型' };
 return { kind: 'ready', label: '已就绪' };
}

export function displayEndpoint(baseUrl: string): string {
 const trimmed = baseUrl.trim();
 if (!trimmed) return '未填写地址';
 try {
  const parsed = new URL(trimmed);
  const path = parsed.pathname.replace(/\/+$/, '');
  if (!path || path === '/v1' || path === '/v1beta') return parsed.host;
  return `${parsed.host}${path}`;
 } catch {
  return trimmed;
 }
}

export function serviceDisplayName(profile: Profile, connections: Connections): string {
 if (profile.preset === 'gateway') {
  return connections.gatewayExamples.find(example => example.icon === profile.icon)?.name || '自定义本机服务';
 }
 if (profile.preset === 'compatible') return '自定义中转站';
 return connections.presets.find(preset => preset.id === profile.preset)?.name || profile.preset;
}

export function buildDirectoryGroups(connections: Connections): {
 id: string;
 name: string;
 hint: string;
 choices: { key: string; profile: Profile; keywords: string }[];
}[] {
 const gateway = connections.presets.find(preset => preset.id === 'gateway');
 const direct = connections.presets.filter(preset => preset.id !== 'gateway' && !relayPresets.has(preset.id));
 const relays = connections.presets.filter(preset => relayPresets.has(preset.id));
 return [
  {
   id: 'local',
   name: '本机服务',
   hint: '本机运行的网关与推理服务，通常无需云端密钥',
   choices: gateway ? [
    ...connections.gatewayExamples.map(example => ({
     key: `gateway:${example.icon}`,
     profile: {
      ...emptyProfile(gateway),
      name: example.name,
      baseUrl: example.baseUrl,
      gatewayApi: example.api,
      icon: example.icon,
     },
     keywords: `${example.name} ${example.icon} ${example.baseUrl} 本机 网关`,
    })),
    {
     key: 'gateway:custom',
     profile: emptyProfile({ ...gateway, name: '自定义本机服务' }),
     keywords: '自定义 本机 网关 local gateway http 127.0.0.1',
    },
   ] : [],
  },
  {
   id: 'direct',
   name: '模型厂商',
   hint: '直连官方 API',
   choices: direct.map(preset => ({
    key: `preset:${preset.id}`,
    profile: emptyProfile(preset),
    keywords: `${preset.name} ${preset.id} ${preset.baseUrl} ${presetKeywords[preset.id] ?? ''}`,
   })),
  },
  {
   id: 'relay',
   name: '聚合与中转',
   hint: '多模型聚合平台或 OpenAI 兼容接口',
   choices: relays.map(preset => {
    const label = preset.id === 'compatible' ? '自定义中转站' : preset.name;
    return {
     key: `preset:${preset.id}`,
     profile: emptyProfile({ ...preset, name: label }),
     keywords: `${label} ${preset.id} ${preset.baseUrl} ${presetKeywords[preset.id] ?? ''}`,
    };
   }),
  },
 ].filter(group => group.choices.length > 0);
}

export function matchesConfiguredChoice(choice: Profile, profiles: Profile[]): boolean {
 return profiles.some(profile => {
  if (profile.disabled) return false;
  if (choice.preset === 'gateway') {
   if (profile.preset !== 'gateway') return false;
   if (choice.icon) return profile.icon === choice.icon;
   return !profile.icon || profile.icon === 'generic';
  }
  return profile.preset === choice.preset;
 });
}

function deriveHostName(url: URL): string {
 const host = url.hostname.replace(/^www\./, '').replace(/^api\./, '');
 const label = host.split('.')[0] || host;
 return label.charAt(0).toUpperCase() + label.slice(1);
}

export function analyzeQuickInput(raw: string, connections: Connections): QuickInputAction | null {
 const text = raw.trim();
 if (!text) return null;

 // Extract URL and optional key if user pasted both together.
 const urlMatch = /https?:\/\/[^\s"'<>]+/i.exec(text);
 const tokens = text.split(/\s+/).filter(Boolean);
 const keyToken = tokens.find(token =>
  !token.startsWith('http://') &&
  !token.startsWith('https://') &&
  /^(?:sk-[A-Za-z0-9_-]{8,}|AIza[A-Za-z0-9_-]{12,}|[A-Za-z0-9_-]{24,}\.[A-Za-z0-9_-]{8,})$/.test(token),
 );

 if (urlMatch) {
  let parsed: URL;
  try {
   parsed = new URL(urlMatch[0]);
  } catch {
   return null;
  }
  const host = parsed.hostname.toLowerCase();
  const isLoopback = host === '127.0.0.1' || host === 'localhost' || host === '::1';

  if (isLoopback) {
   const gateway = connections.presets.find(p => p.id === 'gateway');
   if (!gateway) return null;
   const matchedExample = connections.gatewayExamples.find(example => {
    try {
     return new URL(example.baseUrl).port === parsed.port && parsed.port !== '';
    } catch {
     return false;
    }
   });
   const profile: Profile = matchedExample
    ? {
       ...emptyProfile(gateway),
       name: matchedExample.name,
       baseUrl: urlMatch[0],
       gatewayApi: matchedExample.api,
       icon: matchedExample.icon,
      }
    : {
       ...emptyProfile(gateway),
       name: parsed.port ? `本机服务 :${parsed.port}` : '自定义本机服务',
       baseUrl: urlMatch[0],
       gatewayApi: 'chat',
      };
   return {
    kind: 'url',
    label: matchedExample ? `按本机 ${matchedExample.name} 创建` : '按此本机地址创建网关服务',
    detail: keyToken ? `${displayEndpoint(urlMatch[0])} · 已自动带入密钥` : displayEndpoint(urlMatch[0]),
    profile,
    apiKey: keyToken,
   };
  }

  const matchedPreset = connections.presets.find(preset => {
   if (preset.id === 'minimax' && (host === 'api.minimax.io' || host === 'api.minimax.chat')) return true;
   if (!preset.baseUrl) return false;
   try {
    return new URL(preset.baseUrl).hostname.toLowerCase() === host;
   } catch {
    return false;
   }
  });
  if (matchedPreset) {
   return {
    kind: 'url',
    label: `识别为 ${matchedPreset.name}，立即配置`,
    detail: keyToken ? `${displayEndpoint(urlMatch[0])} · 已自动带入密钥` : displayEndpoint(urlMatch[0]),
    profile: { ...emptyProfile(matchedPreset), baseUrl: urlMatch[0] },
    apiKey: keyToken,
   };
  }

  const compatible = connections.presets.find(p => p.id === 'compatible');
  if (!compatible) return null;
  return {
   kind: 'url',
   label: `用此地址新建自定义提供商（${deriveHostName(parsed)}）`,
   detail: keyToken ? `${displayEndpoint(urlMatch[0])} · 已自动带入密钥` : displayEndpoint(urlMatch[0]),
   profile: {
    ...emptyProfile(compatible),
    name: deriveHostName(parsed),
    baseUrl: urlMatch[0],
    apiProtocol: parsed.pathname.includes('/messages') ? 'anthropic' : 'chat',
   },
   apiKey: keyToken,
  };
 }

 if (keyToken) {
  let presetId = 'compatible';
  let label = '识别到 API 密钥，新建自定义提供商';
  if (keyToken.startsWith('sk-ant-')) {
   presetId = 'anthropic';
   label = '识别到 Anthropic 密钥，立即配置 Anthropic';
  } else if (keyToken.startsWith('sk-or-')) {
   presetId = 'openrouter';
   label = '识别到 OpenRouter 密钥，立即配置 OpenRouter';
  } else if (keyToken.startsWith('AIza')) {
   presetId = 'gemini';
   label = '识别到 Google Gemini 密钥，立即配置 Gemini';
  } else if (keyToken.startsWith('sk-proj-')) {
   presetId = 'openai';
   label = '识别到 OpenAI 密钥，立即配置 OpenAI';
  }
  const preset = connections.presets.find(p => p.id === presetId) ?? connections.presets.find(p => p.id === 'compatible');
  if (!preset) return null;
  return {
   kind: 'key',
   label,
   detail: '已提取密钥，或直接点击下方任意提供商带入此密钥',
   profile: emptyProfile(preset.id === 'compatible' ? { ...preset, name: '自定义中转站' } : preset),
   apiKey: keyToken,
  };
 }

 return null;
}
