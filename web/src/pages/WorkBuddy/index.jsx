/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Divider,
  Empty,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IconCheckCircleStroked,
  IconCopy,
  IconDownload,
  IconInfoCircle,
  IconRefresh,
  IconSend,
  IconTerminal,
} from '@douyinfe/semi-icons';
import { API, copy, showError, showSuccess } from '../../helpers';

const { Title, Text } = Typography;

const DEFAULT_MAX_INPUT = 131072;
const DEFAULT_MAX_OUTPUT = 32768;

// 后端 /api/token/ 返回的 key 是不含前缀的裸密钥（与令牌列表中展示的 sk-xxx 对应），
// 而调用本站 /v1 接口时必须带上 sk- 前缀（见 middleware/auth.go 的 TrimPrefix("sk-")）。
// 因此页面内部统一保存裸 key 用于下拉框比对，仅在写入配置时补上前缀。
const SK_PREFIX = 'sk-';

// 为令牌补上 sk- 前缀；已带前缀或为空则原样返回，避免出现 sk-sk- 或 sk- 空值
const withSkPrefix = (key) => {
  const trimmed = (key || '').trim();
  if (!trimmed) return '';
  return trimmed.startsWith(SK_PREFIX) ? trimmed : SK_PREFIX + trimmed;
};

// 判断服务器地址是否为「只在本机有效」的回环地址。
// 后端 system_setting.ServerAddress 默认值是 http://localhost:3000，
// 且该字段不会为空，因此不能只用 `||` 兜底——否则会把 localhost 导出给别人，
// 别人机器上的 localhost 指向他自己，必然连不上。
const isLocalHostAddress = (addr) => {
  const value = (addr || '').trim().toLowerCase();
  if (!value) return true;
  return (
    value.includes('localhost') ||
    value.includes('127.0.0.1') ||
    value.includes('0.0.0.0') ||
    value.includes('[::1]')
  );
};

// 解析最终用于导出的站点地址：
// 1) 后端已配置为真实域名/公网地址 → 直接使用；
// 2) 后端仍是默认的 localhost（未配置）→ 回退到浏览器当前访问地址，
//    因为用户此刻正是通过可用地址打开本页面的；
// 3) 统一去掉尾部斜杠与可能误填的 /v1、/v1/chat/completions 后缀，
//    避免后续拼接出现 /v1/v1 这类重复后缀。
const resolveServerAddress = (serverAddress) => {
  let base = isLocalHostAddress(serverAddress)
    ? window.location.origin
    : (serverAddress || '').trim();
  base = base.replace(/\/+$/, '');
  base = base.replace(/\/(v1\/chat\/completions|v1)$/i, '');
  return base.replace(/\/+$/, '');
};

// 依据站点地址推断 WorkBuddy 需要的完整 chat/completions endpoint。
// 说明：WorkBuddy 的 vendor=Custom 走 OpenAI 兼容的「完整 endpoint」模式，
// 它不会自行拼接路径，而是原样向 url 发请求，因此必须带上 /v1/chat/completions，
// 这点与只填 base_url 的客户端不同（参见官方 provider 注册表中 openrouter 的写法）。
const buildChatCompletionsUrl = (serverAddress) => {
  const base = resolveServerAddress(serverAddress);
  if (!base) return '';
  return `${base}/v1/chat/completions`;
};

// 一键导入使用的自定义 URL 协议（与后端 controller.WorkBuddyProtocol 保持一致）
const WORKBUDDY_PROTOCOL = 'workbuddy-import';
// 安装器下载地址（后端生成）
const WORKBUDDY_INSTALLER_URL = '/api/workbuddy/installer';

// 以 UTF-8 编码对字符串做 base64（保证中文等非 ASCII 字符安全）
const base64Utf8 = (str) => {
  const bytes = new TextEncoder().encode(str);
  let binary = '';
  bytes.forEach((b) => {
    binary += String.fromCharCode(b);
  });
  return btoa(binary);
};

// 将标准 base64 转为 base64url（去掉 = 填充，+/ 换成 -_）。
// 关键：自定义协议 URL 会经由 Windows Shell 传递到本地组件，
// 若载荷含 "="（标准 base64 的填充）会被 URL 编码成 %3D，该转义在传递过程中可能被破坏，
// 导致本地组件解码失败并写出空文件。改用 base64url 后载荷只含 [A-Za-z0-9-_]，
// 无需再做任何百分号编码，可彻底避免该问题。
const toBase64Url = (b64) => b64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');

const WorkBuddy = () => {
  const [loading, setLoading] = useState(true);
  const [models, setModels] = useState([]);
  const [selectedIds, setSelectedIds] = useState([]);
  const [modelSettings, setModelSettings] = useState({});
  const [tokens, setTokens] = useState([]);
  const [apiUrl, setApiUrl] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [keyword, setKeyword] = useState('');
  // 是否启用「仅国内模型」限制。由后端返回，前端只做展示与二次过滤，
  // 真正的拦截在后端接口层（境外模型根本不会下发到前端）。
  const [domesticOnly, setDomesticOnly] = useState(true);
  // 被后端过滤掉的境外模型数量，用于向用户解释「为什么模型变少了」
  const [filteredForeignCount, setFilteredForeignCount] = useState(0);

  const loadToken = useCallback(async (applyFirst = true) => {
    try {
      const res = await API.get('/api/token/', {
        params: { page: 1, page_size: 100 },
        disableDuplicate: true,
      });
      const items = Array.isArray(res.data.data)
        ? res.data.data
        : res.data.data?.items || [];
      const usable = items.filter((tk) => tk.status === 1 && tk.key);
      setTokens(usable);
      if (applyFirst && usable.length > 0) {
        setApiKey(usable[0].key);
      }
    } catch (err) {
      console.error('加载令牌失败', err);
    }
  }, []);

  useEffect(() => {
    const load = async () => {
      setLoading(true);
      try {
        const res = await API.get('/api/workbuddy/models', {
          disableDuplicate: true,
        });
        const data = res.data.data || {};
        // 后端在开启「仅国内模型」时已过滤掉境外模型；
        // 这里再做一次前端兜底过滤，防止后端开关被关闭时前端仍误放。
        const domesticOnlyFlag = data.domestic_only !== false;
        const rawList = Array.isArray(data.models) ? data.models : [];
        const modelList = domesticOnlyFlag
          ? rawList.filter((m) => m.origin !== 'foreign')
          : rawList;
        setDomesticOnly(domesticOnlyFlag);
        setFilteredForeignCount(data.filtered_foreign_count ?? 0);
        setModels(modelList);

        const settings = {};
        modelList.forEach((m) => {
          settings[m.id] = {
            supportsToolCall: m.supports_tool_call ?? true,
            supportsImages: m.supports_images ?? false,
            supportsReasoning: m.supports_reasoning ?? false,
            maxInputTokens: DEFAULT_MAX_INPUT,
            maxOutputTokens: DEFAULT_MAX_OUTPUT,
          };
        });
        setModelSettings(settings);
        setSelectedIds(modelList.map((m) => m.id));

        // 站点地址解析规则见 resolveServerAddress / buildChatCompletionsUrl 的注释：
        // 后端未配置时（默认 localhost）回退到当前浏览器地址，避免导出错误地址；
        // 并统一补齐 WorkBuddy 要求的完整 chat/completions endpoint。
        setApiUrl(buildChatCompletionsUrl(data.server_address));
      } catch (err) {
        showError(err?.response?.data?.message || err?.message || '加载模型失败');
      } finally {
        setLoading(false);
      }
    };
    load();
    loadToken();
  }, [loadToken]);

  // 安装器运行完毕会带 #wb-installed=1 跳回本页：主动报喜并指明下一步，
  // 避免小白“装完不知道装没装好”。
  useEffect(() => {
    if (window.location.hash === '#wb-installed=1') {
      window.history.replaceState(null, '', window.location.pathname + window.location.search);
      showSuccess('导入组件安装完成！现在点击下方【一键导入 WorkBuddy】即可');
    }
  }, []);

  const updateSetting = (id, key, value) => {
    setModelSettings((prev) => ({
      ...prev,
      [id]: { ...prev[id], [key]: value },
    }));
  };

  const filteredModels = useMemo(() => {
    const kw = (keyword || '').trim().toLowerCase();
    if (!kw) return models;
    return models.filter(
      (m) =>
        m.id.toLowerCase().includes(kw) ||
        (m.vendor || '').toLowerCase().includes(kw),
    );
  }, [models, keyword]);

  const buildConfig = useCallback(() => {
    // 字段与 WorkBuddy「设置 → 模型设置」表单产物保持一致
    // （isValidLocalCustomModel 校验：id 必填，其余字段类型可选）
    return selectedIds.map((id) => {
      const s = modelSettings[id] || {};
      return {
        id,
        name: id,
        vendor: 'Custom',
        url: apiUrl.trim(),
        apiKey: withSkPrefix(apiKey),
        supportsToolCall: !!s.supportsToolCall,
        supportsImages: !!s.supportsImages,
        supportsReasoning: !!s.supportsReasoning,
        useCustomProtocol: false,
        maxInputTokens: Number(s.maxInputTokens) || DEFAULT_MAX_INPUT,
        maxOutputTokens: Number(s.maxOutputTokens) || DEFAULT_MAX_OUTPUT,
      };
    });
  }, [selectedIds, modelSettings, apiUrl, apiKey]);

  const previewJson = useMemo(
    () => JSON.stringify(buildConfig(), null, 2),
    [buildConfig],
  );

  const handleCopy = async () => {
    if (selectedIds.length === 0) {
      showError('请先勾选要导出的模型');
      return;
    }
    try {
      await copy(previewJson);
      showSuccess('配置已复制到剪贴板');
    } catch (err) {
      showError('复制失败：' + (err?.message || ''));
    }
  };

  const handleDownloadJson = () => {
    if (selectedIds.length === 0) {
      showError('请先勾选要导出的模型');
      return;
    }
    const blob = new Blob([previewJson], { type: 'application/json' });
    const objectUrl = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = objectUrl;
    a.download = 'models.json';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(objectUrl);
    showSuccess('models.json 已下载');
  };

  // 构建自定义协议 URL：workbuddy-import://import?d=<base64url配置>
  // 使用 base64url 且不做百分号编码：载荷仅含 [A-Za-z0-9-_]，
  // 可安全穿过 Windows Shell 的参数传递，避免 %3D 等转义被破坏。
  const buildProtocolUrl = useCallback((json) => {
    const payload = toBase64Url(base64Utf8(json));
    return `${WORKBUDDY_PROTOCOL}://import?d=${payload}`;
  }, []);

  // 唤起本地接收器（通过 mshta 执行 import.hta 完成写入）。
  // 浏览器无法得知协议是否真的执行成功，因此不做不可靠的“失焦检测”，
  // 而是直接唤起；若本机尚未安装组件，浏览器会静默失败，由用户反馈后走安装引导。
  const invokeProtocol = useCallback((protocolUrl) => {
    const iframe = document.createElement('iframe');
    iframe.style.display = 'none';
    iframe.src = protocolUrl;
    document.body.appendChild(iframe);
    setTimeout(() => {
      if (iframe.parentNode) {
        iframe.parentNode.removeChild(iframe);
      }
    }, 3000);
  }, []);

  // 下载安装器（仅需一次）。浏览器沙箱不允许网页打开本地文件夹，
  // 因此下载后立即弹出图文步骤，指引用户直接点浏览器下载栏/下载气泡里的文件——
  // 这比"去下载文件夹找文件"更少操作。
  const handleDownloadInstaller = useCallback(async () => {
    try {
      const u = new URL(window.location.href);
      u.hash = 'wb-installed=1';
      const res = await API.get(WORKBUDDY_INSTALLER_URL, {
        responseType: 'blob',
        disableDuplicate: true,
        params: { redirect: u.toString() },
      });
      const blob = new Blob([res.data], { type: 'application/bat' });
      const objectUrl = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = objectUrl;
      a.download = 'workbuddy-install.bat';
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(objectUrl);
      Modal.info({
        title: '文件已在下载，就差最后 3 小步',
        content: (
          <div style={{ lineHeight: 2, fontSize: 13 }}>
            <div>
              <b>第 1 步：</b>看屏幕【右上角】——浏览器会弹出下载提示（一个向下的箭头
              <IconDownload size='small' style={{ margin: '0 4px' }} />
              图标）；如果屏幕【底部】出现下载条，直接点文件名也一样。
            </div>
            <div>
              <b>第 2 步：</b>点击 <Tag size='small'>workbuddy-install.bat</Tag>
              （若浏览器提示「此文件可能有害」，请选择【保留】）
            </div>
            <div>
              <b>第 3 步：</b>会闪出一个黑色小窗口，<b>不用按任何键</b>，约 3
              秒自动装完并跳回本页。
            </div>
            <Divider margin={12} />
            <Text type='tertiary' style={{ fontSize: 12 }}>
              回到本页后，再点一次【一键导入 WorkBuddy】即可。
              这次装好后，以后导入永远一键完成，不再下载任何东西。
            </Text>
          </div>
        ),
        okText: '知道了，我这就去点',
      });
    } catch (err) {
      showError('下载安装组件失败：' + (err?.message || ''));
    }
  }, []);

  // 首次使用引导：用大白话讲清楚“为什么装、装一次管多久、怎么装”
  const showInstallGuide = useCallback(() => {
    Modal.confirm({
      title: '第一次使用，先花 10 秒装个小组件（只装这一次）',
      content: (
        <div style={{ lineHeight: 2, fontSize: 13 }}>
          <b>为什么要装？</b>
          <br />
          浏览器出于安全禁止网页直接写你电脑里的文件，所以需要一个极小（约 40
          KB）的本地组件代写。
          <br />
          <b>装好之后呢？</b>
          <br />
          以后每次导入只点一下【一键导入 WorkBuddy】，永远不再下载、不再安装。
          <br />
          <b>怎么装？</b>
          <br />
          点下面按钮下载 → 双击运行 → 自动跳回本页。全程无需管理员权限。
          <br />
          <Text type='tertiary' style={{ fontSize: 12 }}>
            组件只写入 WorkBuddy 的配置文件，可随时卸载（删除注册表项
            HKCU\Software\Classes\workbuddy-import 即可）。
          </Text>
        </div>
      ),
      okText: '下载安装组件',
      cancelText: '取消',
      onOk: () => handleDownloadInstaller(),
    });
  }, [handleDownloadInstaller]);

  // 一键导入：直接唤起本地接收器写入配置；未安装时提示安装
  const handleOneClickImport = useCallback(async () => {
    if (selectedIds.length === 0) {
      showError('请先勾选要导入的模型');
      return;
    }
    if (!apiKey.trim()) {
      showError('请先选择 API 密钥');
      return;
    }

    const json = previewJson;
    const protocolUrl = buildProtocolUrl(json);

    // 需要用户主动确认一次（浏览器安全策略），确认后立即唤起本地组件
    Modal.confirm({
      title: '确认导入到 WorkBuddy？',
      content: (
        <div style={{ lineHeight: 1.9, fontSize: 13 }}>
          即将把 <b>{selectedIds.length}</b> 个模型配置写入本机 WorkBuddy。
          <br />
          旧配置会自动备份为 <Tag size='small'>models.json.backup</Tag>。
          <br />
          <br />
          <Text type='tertiary' style={{ fontSize: 12 }}>
            若浏览器弹出「要允许此网站打开 WorkBuddy 导入工具吗？」→ 请点【打开】。
            <br />
            第一次使用？若点完毫无动静，说明组件还没装——马上会弹出安装向导，跟着点就行。
          </Text>
        </div>
      ),
      okText: '开始导入',
      cancelText: '取消',
      onOk: () => {
        invokeProtocol(protocolUrl);
        setTimeout(() => {
          Modal.confirm({
            title: '请到 WorkBuddy 里确认一下',
            content: (
              <div style={{ lineHeight: 2, fontSize: 13 }}>
                配置已发往本机。查看结果只需 3 步：
                <br />
                <b>1.</b> 打开 WorkBuddy（桌面或开始菜单里）
                <br />
                <b>2.</b> 进入 设置 → 模型设置
                <br />
                <b>3.</b> 若该页面本来就开着，先关掉再重新打开（它只在打开时读取配置）
                <br />
                <br />
                看到了刚选的模型 → 点【已看到，完成】。
                <br />
                没看到，或刚才全程毫无动静 → 组件还没装好，点【没看到，去安装】。
                <br />
                <Text type='tertiary' style={{ fontSize: 12 }}>
                  提示：导入是覆盖式的，旧配置已自动备份为 models.json.backup。
                </Text>
              </div>
            ),
            okText: '已看到，完成',
            cancelText: '没看到，去安装',
            onCancel: () => showInstallGuide(),
          });
        }, 1500);
      },
    });
  }, [
    selectedIds,
    apiKey,
    previewJson,
    buildProtocolUrl,
    invokeProtocol,
    showInstallGuide,
  ]);

  const columns = [
    {
      title: '模型名称',
      dataIndex: 'id',
      key: 'id',
      width: 220,
      render: (text) => <Text strong>{text}</Text>,
    },
    {
      title: '供应商',
      dataIndex: 'vendor',
      key: 'vendor',
      width: 130,
      render: (text, record) => {
        const vendorName = text || record.vendor;
        if (!vendorName) {
          // 未能识别供应商时，说明该模型是靠「模型名关键词」判定为国内
          return (
            <Tag size='small' color='grey'>
              未知
            </Tag>
          );
        }
        return (
          <Tag size='small' color='green'>
            {vendorName}
          </Tag>
        );
      },
    },
    {
      title: '工具调用',
      dataIndex: 'tool_call',
      key: 'tool_call',
      width: 120,
      render: (_, record) => (
        <Switch
          size='small'
          checked={!!modelSettings[record.id]?.supportsToolCall}
          onChange={(v) => updateSetting(record.id, 'supportsToolCall', v)}
        />
      ),
    },
    {
      title: '图片输入',
      dataIndex: 'images',
      key: 'images',
      width: 120,
      render: (_, record) => (
        <Switch
          size='small'
          checked={!!modelSettings[record.id]?.supportsImages}
          onChange={(v) => updateSetting(record.id, 'supportsImages', v)}
        />
      ),
    },
    {
      title: '推理',
      dataIndex: 'reasoning',
      key: 'reasoning',
      width: 110,
      render: (_, record) => (
        <Switch
          size='small'
          checked={!!modelSettings[record.id]?.supportsReasoning}
          onChange={(v) => updateSetting(record.id, 'supportsReasoning', v)}
        />
      ),
    },
    {
      title: '输入上限',
      dataIndex: 'max_input',
      key: 'max_input',
      width: 150,
      render: (_, record) => (
        <InputNumber
          size='small'
          min={1}
          value={modelSettings[record.id]?.maxInputTokens}
          onChange={(v) => updateSetting(record.id, 'maxInputTokens', v)}
          style={{ width: 120 }}
        />
      ),
    },
    {
      title: '输出上限',
      dataIndex: 'max_output',
      key: 'max_output',
      width: 150,
      render: (_, record) => (
        <InputNumber
          size='small'
          min={1}
          value={modelSettings[record.id]?.maxOutputTokens}
          onChange={(v) => updateSetting(record.id, 'maxOutputTokens', v)}
          style={{ width: 120 }}
        />
      ),
    },
  ];

  return (
    <div className='mt-[60px] p-2 max-w-[1200px] mx-auto'>
      <Title heading={3} style={{ marginBottom: 8 }}>
        WorkBuddy 一键部署
      </Title>
      <Banner
        type='info'
        icon={<IconInfoCircle />}
        description='将本面板（batapi）的模型一键导出为 WorkBuddy 客户端可用的模型配置，并自动生成 models.json 或一键部署脚本。部署后 WorkBuddy 将使用你下方的 API 地址与密钥进行调用。'
      />
      <div className='flex flex-col gap-4 mt-4'>
        <Card header='1. 连接配置'>
          <div className='grid gap-4 md:grid-cols-2'>
            <div>
              <Text strong>API 地址（本站，已固定）</Text>
              <div style={{ marginTop: 6, display: 'flex', alignItems: 'center', gap: 8 }}>
                <Tag color='green' size='large' prefixIcon={<IconCheckCircleStroked />}>
                  {apiUrl || '加载中...'}
                </Tag>
              </div>
              <Text
                type='tertiary'
                style={{ fontSize: 12, marginTop: 4, display: 'block' }}
              >
                自动使用当前站点地址，无需填写、也无法修改。
                <br />
                WorkBuddy 自定义模型需要完整路径，因此会带上{' '}
                <Tag size='small'>/v1/chat/completions</Tag>（这是它要求的格式，不能省略）。
                <br />
                若后台「运营设置 → 系统」里的服务器地址仍为默认的 localhost，
                这里会自动改用你当前浏览器访问的地址，方便你直接使用。
              </Text>
            </div>
            <div>
              <Text strong>API 密钥</Text>
              <div className='flex gap-2 items-start'>
                <Select
                  placeholder='从我的令牌中选择'
                  optionList={tokens.map((tk) => ({
                    label: `${tk.name}${tk.unlimited_quota ? '（不限量）' : ''}`,
                    value: tk.key,
                  }))}
                  value={tokens.some((tk) => tk.key === apiKey) ? apiKey : undefined}
                  onChange={(v) => v && setApiKey(v)}
                  style={{ width: 260, marginTop: 6 }}
                  emptyContent='暂无可用令牌，请先到「令牌」页面创建'
                />
                <Button
                  icon={<IconRefresh />}
                  style={{ marginTop: 6 }}
                  onClick={() => loadToken(false)}
                >
                  刷新
                </Button>
              </div>
              <Text
                type='tertiary'
                style={{ fontSize: 12, marginTop: 4, display: 'block' }}
              >
                从你的令牌列表中选择一个即可，导入后 WorkBuddy 将用它调用本站模型。
                <br />
                实际写入配置的密钥会自动补上 <Tag size='small'>sk-</Tag> 前缀：
                <Tag size='small'>{withSkPrefix(apiKey) ? `${withSkPrefix(apiKey).slice(0, 7)}***` : '未选择'}</Tag>
              </Text>
            </div>
          </div>
        </Card>

        <Card header='2. 选择模型'>
          {domesticOnly && (
            <Banner
              type='warning'
              icon={<IconInfoCircle />}
              description={`为符合合规要求，本页仅可选择国内（境内）厂商的模型${
                filteredForeignCount > 0
                  ? `，已自动隐藏 ${filteredForeignCount} 个境外模型`
                  : ''
              }。若某个国内模型未被显示，请联系管理员将模型名加入放行名单。`}
              style={{ marginBottom: 12 }}
            />
          )}
          <div className='flex flex-wrap gap-2 items-center mb-3'>
            <Input
              placeholder='搜索模型名称或供应商'
              value={keyword}
              onChange={(v) => setKeyword(v)}
              style={{ width: 240 }}
            />
            <Button
              size='small'
              onClick={() => setSelectedIds(filteredModels.map((m) => m.id))}
            >
              全选
            </Button>
            <Button size='small' onClick={() => setSelectedIds([])}>
              清空
            </Button>
            <Tag color='blue' style={{ marginLeft: 'auto' }}>
              已选 {selectedIds.length} / {filteredModels.length} 个模型
            </Tag>
          </div>
          <Spin spinning={loading}>
            <Table
              columns={columns}
              dataSource={filteredModels}
              rowKey='id'
              pagination={false}
              rowSelection={{
                selectedRowKeys: selectedIds,
                onChange: (keys) => setSelectedIds(keys),
              }}
              empty={
                <Empty
                  title='暂无可用模型'
                  description={
                    domesticOnly
                      ? '该账号分组下的模型均不属于国内（境内）厂商，或未被识别。请联系管理员在「系统设置」中将模型名加入 WorkBuddy 放行名单。'
                      : '该账号分组下没有可用的模型'
                  }
                />
              }
              scroll={{ x: 'max-content' }}
            />
          </Spin>
        </Card>

        <Card
          header='3. 一键导入'
          headerExtraContent={
            <Space>
              <Button
                theme='borderless'
                icon={<IconCopy />}
                onClick={handleCopy}
                disabled={selectedIds.length === 0}
              >
                复制配置
              </Button>
              <Button
                theme='borderless'
                icon={<IconDownload />}
                onClick={handleDownloadJson}
                disabled={selectedIds.length === 0}
              >
                下载 models.json
              </Button>
            </Space>
          }
        >
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              gap: 12,
              padding: '16px 0',
            }}
          >
            <Button
              theme='solid'
              type='primary'
              size='large'
              icon={<IconSend />}
              onClick={handleOneClickImport}
              disabled={selectedIds.length === 0 || !apiKey}
              style={{ minWidth: 240, height: 46, fontSize: 16 }}
            >
              一键导入 WorkBuddy
            </Button>
            <Text type='tertiary' style={{ fontSize: 13, textAlign: 'center' }}>
              点击后自动把上面选中的 <b>{selectedIds.length}</b> 个模型写入本机
              WorkBuddy 配置
              <br />
              首次使用需安装一次导入组件（约 3 秒），之后全部一键完成。
            </Text>
            <Button
              theme='borderless'
              size='small'
              icon={<IconTerminal />}
              onClick={handleDownloadInstaller}
            >
              重新下载导入组件（首次安装用）
            </Button>
          </div>

          <Divider margin={12} />

          <Text type='tertiary' style={{ fontSize: 12 }}>
            配置预览（点击「一键导入」后写入本机的内容）：
          </Text>
          <pre
            style={{
              maxHeight: 320,
              overflow: 'auto',
              background: 'var(--semi-color-fill-0)',
              padding: 12,
              borderRadius: 6,
              fontSize: 12,
              lineHeight: 1.6,
              marginTop: 8,
              marginBottom: 0,
            }}
          >
            {previewJson}
          </pre>
        </Card>

        <Card header='4. 使用说明'>
          <div className='grid gap-2 md:grid-cols-2'>
            <div>
              <Text strong style={{ display: 'block', marginBottom: 6 }}>
                一键导入（推荐）
              </Text>
              <Text type='tertiary' style={{ fontSize: 13, lineHeight: 1.8 }}>
                1. 选择 API 密钥、勾选要用的模型；
                <br />
                2. 点击 <Tag size='small'>一键导入 WorkBuddy</Tag>；
                <br />
                3. <b>首次</b>会提示安装组件，下载后双击运行一次；
                <br />
                4. 再次点击「一键导入」→ 配置自动写入本机；
                <br />
                5. 打开 WorkBuddy → 设置 → 模型设置，即可看到导入的模型
                （页面打开时会重新读取，若已打开请先关闭再重新打开，无需重启）。
              </Text>
            </div>
            <div>
              <Text strong style={{ display: 'block', marginBottom: 6 }}>
                手动方式（备用）
              </Text>
              <Text type='tertiary' style={{ fontSize: 13, lineHeight: 1.8 }}>
                1. 点击「下载 models.json」；
                <br />
                2. Windows 下放到{' '}
                <Tag size='small'>%USERPROFILE%\.workbuddy\models.json</Tag>{' '}
                （macOS/Linux 为 ~/.workbuddy/models.json），覆盖旧文件；
                <br />
                3. 打开 WorkBuddy → 设置 → 模型设置，在列表中选择模型即可
                （若设置页已打开，请先关闭再重新打开）。
              </Text>
            </div>
          </div>
          <Divider margin={12} />
          <Text type='tertiary' style={{ fontSize: 13, lineHeight: 1.8 }}>
            提示：以上配置直接调用本面板的 /v1 接口，所有模型共用同一个令牌；模型能力开关（工具调用 /
            图片输入 / 推理）已按常见规则自动推断，可按实际情况手动调整。若你的令牌设置了模型白名单，可在导出时额外加入
            「Auto」模型，WorkBuddy 会按请求自动匹配合适模型。
          </Text>
        </Card>
      </div>
    </div>
  );
};

export default WorkBuddy;