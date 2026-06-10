const { createApp } = Vue;

createApp({
  data() {
    return {
      activeTab: 'usage',
      // Usage tab state
      days: 30, streamMode: true, autoRefresh: true, refreshSeconds: 5,
      refreshTimerId: null, stream: null, loading: false,
      summary: {
        total_tokens: 0, total_requests: 0, success_requests: 0, success_rate: 0,
        estimated_cost_usd: 0, prompt_tokens: 0, completion_tokens: 0,
        avg_latency_ms: 0, generated_at: "", daily: [], by_model: [], recent: [],
      },
      // Export tab state
      exportFormat: 'reasoning',
      exportDates: [], exportLoading: false,
      uploadingToMS: false, uploadingToHF: false,
      // Conversation browser tab state (DeepSeek style)
      convGroups: [], convLoading: false,
      selectedConv: null, selectedConvId: null,
      convDetailLoading: false, collapsedThinking: {},
      toast: { show: false, message: '', type: 'success' },
      // Agent tab state
      agentModel: '', agentModels: [],
      agentTools: { bash: true, read_file: true, write_file: true, grep: true, glob: true },
      agentMessages: [], agentInput: '', agentLoading: false,
    };
  },
  computed: {
    chartData() {
      const daily = this.summary.daily || [];
      const max = daily.reduce((m, p) => Math.max(m, p.total_tokens || 0, 1), 1);
      return daily.map((p) => {
        const total = p.total_tokens || 1;
        return {
          ...p, height: Math.max(2, (p.total_tokens / max) * 100),
          prompt_ratio: p.prompt_tokens / total, completion_ratio: p.completion_tokens / total,
          shortDate: (p.date || "").slice(5), dateLabel: p.date || "",
        };
      });
    },
    chartMax() {
      return (this.summary.daily || []).reduce((m, p) => Math.max(m, p.total_tokens || 0), 0) || 1;
    },
  },
  methods: {
    Math: window.Math,
    fmtInt(v) { return Number(v || 0).toLocaleString("zh-CN"); },
    fmtCost(v) { const n = Number(v || 0); if (n < 0.01) return n.toFixed(4); if (n < 1) return n.toFixed(3); return n.toFixed(2); },
    fmtLatency(ms) { const n = Number(ms || 0); return n < 1000 ? n + 'ms' : (n / 1000).toFixed(1) + 's'; },
    fmtTime(ts) {
      if (!ts) return '-';
      const d = new Date(ts), pad = (n) => String(n).padStart(2, '0');
      return pad(d.getMonth()+1) + '/' + pad(d.getDate()) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
    },
    timeAgo(ts) {
      if (!ts) return '-';
      const diff = Date.now() - new Date(ts).getTime(), sec = Math.floor(diff / 1000);
      if (sec < 60) return sec + 's ago'; if (sec < 3600) return Math.floor(sec/60) + 'm ago';
      if (sec < 86400) return Math.floor(sec/3600) + 'h ago'; return Math.floor(sec/86400) + 'd ago';
    },
    shortModel(m) { if (!m) return 'unknown'; return m.replace(/^claude-/, 'claude.').replace(/^gpt-/, 'gpt.'); },
    shortEndpoint(e) { if (!e) return '-'; const parts = e.split('/'); return parts[parts.length-1] || e; },
    ratio(a, b) { if (!b) return 0; return Math.round((a / b) * 100); },
    fmtExportDate(d) { if (!d || d.length !== 8) return d; return d.slice(0,4) + '-' + d.slice(4,6) + '-' + d.slice(6,8); },

    // === Usage tab ===
    async load() {
      this.loading = true;
      try {
        const res = await fetch(`/api/usage/summary?days=${this.days}&t=${Date.now()}`, { cache: "no-store" });
        if (!res.ok) throw new Error("Failed to fetch usage data");
        this.summary = await res.json();
      } catch (err) { console.error(err); }
      finally { this.loading = false; }
    },
    startAutoRefresh() { if (this.streamMode) return; this.stopAutoRefresh(); this.refreshTimerId = setInterval(() => this.load().catch(console.error), this.refreshSeconds * 1000); },
    stopAutoRefresh() { if (this.refreshTimerId) { clearInterval(this.refreshTimerId); this.refreshTimerId = null; } },
    startStream() {
      this.stopStream();
      const es = new EventSource(`/api/usage/stream?days=${this.days}`);
      es.addEventListener("usage", (evt) => { try { this.summary = JSON.parse(evt.data); } catch (err) { console.error(err); } });
      es.onerror = () => { this.streamMode = false; this.stopStream(); };
      this.stream = es;
    },
    stopStream() { if (this.stream) { this.stream.close(); this.stream = null; } },
    toggleStreamMode() { this.streamMode = !this.streamMode; if (this.streamMode) { this.stopAutoRefresh(); this.startStream(); } else { this.stopStream(); this.startAutoRefresh(); } },
    onDaysChange() { this.load().catch(console.error); if (this.streamMode) { this.stopStream(); this.startStream(); } },

    // === Export tab ===
    showToast(msg, type) {
      this.toast = { show: true, message: msg, type: type };
      setTimeout(() => { this.toast.show = false; }, 3500);
    },
    async loadExportDates() {
      this.exportLoading = true;
      try {
        const res = await fetch('/api/export/dates?t=' + Date.now(), { cache: "no-store" });
        if (!res.ok) throw new Error("Failed to load dates");
        const data = await res.json();
        const oldMap = {};
        this.exportDates.forEach(d => { oldMap[d.date] = d.exporting; });
        this.exportDates = (data.dates || []).map(d => ({ ...d, exporting: !!oldMap[d.date] }));
      } catch (err) {
        console.error(err);
        this.showToast('加载日期列表失败: ' + err.message, 'error');
      } finally { this.exportLoading = false; }
    },
    async triggerExport(d) {
      d.exporting = true;
      try {
        const res = await fetch('/api/export/day?date=' + d.date + '&format=' + this.exportFormat, { method: 'POST', cache: "no-store" });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || 'Export failed');
        d.exported = true;
        d.export_file = data.file;
        this.showToast(d.date + ' 导出完成 — ' + this.fmtInt(data.rows) + ' 条记录', 'success');
      } catch (err) {
        console.error(err);
        this.showToast(d.date + ' 导出失败: ' + err.message, 'error');
      } finally { d.exporting = false; }
    },
    downloadExport(fileName) {
      window.open('/api/export/download?file=' + encodeURIComponent(fileName), '_blank');
    },
    async uploadToModelScope() {
      this.uploadingToMS = true;
      try {
        const res = await fetch('/api/modelscope/upload', { method: 'POST', cache: "no-store" });
        const data = await res.json();
        if (res.ok) {
          this.showToast('ModelScope 上传已触发，后台执行中...', 'success');
        } else {
          this.showToast('上传失败: ' + (data.message || data.error || res.statusText), 'error');
        }
      } catch (err) {
        this.showToast('上传请求失败: ' + err.message, 'error');
      } finally { this.uploadingToMS = false; }
    },
    async uploadToHuggingFace() {
      this.uploadingToHF = true;
      try {
        const res = await fetch('/api/huggingface/upload', { method: 'POST', cache: "no-store" });
        const data = await res.json();
        if (res.ok) {
          this.showToast('HuggingFace 上传已触发，后台执行中...', 'success');
        } else {
          this.showToast('上传失败: ' + (data.message || data.error || res.statusText), 'error');
        }
      } catch (err) {
        this.showToast('上传请求失败: ' + err.message, 'error');
      } finally { this.uploadingToHF = false; }
    },

    // === Conversation Browser (DeepSeek Style) ===
    fmtDateShort(ts) {
      if (!ts) return '';
      const d = new Date(ts), pad = n => String(n).padStart(2, '0');
      return pad(d.getMonth()+1) + '/' + pad(d.getDate()) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
    },

    async loadPromptDates() {
      this.loadConversations();
    },

    async loadConversations() {
      this.convLoading = true;
      try {
        const res = await fetch('/api/prompts/conversations?t=' + Date.now(), { cache: 'no-store' });
        if (!res.ok) throw new Error('Failed to load conversations');
        const data = await res.json();
        this.convGroups = data.groups || [];
      } catch (err) {
        console.error(err);
        this.showToast('加载对话列表失败: ' + err.message, 'error');
      } finally { this.convLoading = false; }
    },

    async selectConversation(id) {
      this.selectedConvId = id;
      this.selectedConv = null;
      this.collapsedThinking = {};
      this.convDetailLoading = true;
      try {
        const res = await fetch('/api/prompts/conversation?id=' + encodeURIComponent(id) + '&t=' + Date.now(), { cache: 'no-store' });
        if (!res.ok) throw new Error('Failed to load conversation');
        this.selectedConv = await res.json();
        if (this.selectedConv?.turns) {
          this.selectedConv.turns.forEach(t => {
            if (t.thinking && t.thinking.length > 800) {
              this.collapsedThinking[t.turn_index] = true;
            }
          });
        }
      } catch (err) {
        console.error(err);
        this.showToast('加载对话详情失败: ' + err.message, 'error');
      } finally {
        this.convDetailLoading = false;
        this.$nextTick(() => this.scrollConvToBottom());
      }
    },

    toggleThinking(turnIndex) {
      this.collapsedThinking[turnIndex] = !this.collapsedThinking[turnIndex];
    },

    scrollConvToBottom() {
      const el = this.$refs.convMessages;
      if (el) el.scrollTop = el.scrollHeight;
    },

    async copyText(text) {
      try {
        await navigator.clipboard.writeText(text);
        this.showToast('已复制到剪贴板', 'success');
      } catch {
        const ta = document.createElement('textarea');
        ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
        document.body.appendChild(ta); ta.select();
        document.execCommand('copy'); document.body.removeChild(ta);
        this.showToast('已复制到剪贴板', 'success');
      }
    },

    // === Agent tab ===
    initAgentModels() {
      fetch('/v1/models?t=' + Date.now(), { cache: "no-store" })
        .then(r => r.json())
        .then(data => {
          const models = data.data || data.models || [];
          this.agentModels = models.map(m => typeof m === 'string' ? m : (m.id || m.name || '')).filter(Boolean);
          if (this.agentModels.length > 0 && !this.agentModel) this.agentModel = this.agentModels[0];
        })
        .catch(() => {
          this.agentModels = ['DeepSeekV4'];
          if (!this.agentModel) this.agentModel = 'DeepSeekV4';
        });
    },
    clearAgentChat() { this.agentMessages = []; },
    scrollAgentToBottom() {
      this.$nextTick(() => {
        const el = this.$refs.agentChat;
        if (el) el.scrollTop = el.scrollHeight;
      });
    },
    async sendAgentMessage() {
      const msg = this.agentInput.trim();
      if (!msg || this.agentLoading) return;
      if (!this.agentModel) {
        this.showToast('请先选择模型', 'error');
        return;
      }
      const enabledTools = Object.entries(this.agentTools).filter(([, v]) => v).map(([k]) => k);
      if (enabledTools.length === 0) {
        this.showToast('请至少启用一个工具', 'error');
        return;
      }

      this.agentMessages.push({ role: 'user', content: msg });
      this.agentInput = '';
      this.agentLoading = true;
      this.scrollAgentToBottom();

      try {
        const res = await fetch('/api/chat/agent', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ model: this.agentModel, message: msg, tools: enabledTools }),
        });

        if (!res.ok) {
          const errText = await res.text();
          this.agentMessages.push({ role: 'error', content: '请求失败: ' + (errText || res.statusText) });
          this.agentLoading = false;
          return;
        }

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = '';

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });

          const lines = buf.split('\n');
          buf = '';
          let eventType = '';
          for (let i = 0; i < lines.length; i++) {
            const line = lines[i];
            if (line.startsWith('event: ')) {
              eventType = line.slice(7).trim();
            } else if (line.startsWith('data: ')) {
              const dataStr = line.slice(6);
              try {
                const data = JSON.parse(dataStr);
                switch (eventType) {
                  case 'thinking':
                    this.agentMessages.push({ role: 'thinking', iteration: data.iteration, content: data.thought, collapsed: false });
                    break;
                  case 'action':
                    this.agentMessages.push({ role: 'action', iteration: data.iteration, tool: data.tool, args: data.arguments, descriptor: data.descriptor });
                    break;
                  case 'observation':
                    this.agentMessages.push({ role: 'observation', iteration: data.iteration, content: data.result, collapsed: true });
                    break;
                  case 'response':
                    this.agentMessages.push({ role: 'response', content: data.content });
                    break;
                  case 'done':
                    break;
                  case 'error':
                    this.agentMessages.push({ role: 'error', content: data.message });
                    break;
                }
              } catch (e) {
                buf = line + '\n' + lines.slice(i + 1).join('\n');
                i = lines.length;
              }
              eventType = '';
            } else if (line.trim() !== '') {
              buf += line + '\n';
            }
          }
          this.scrollAgentToBottom();
        }
      } catch (err) {
        console.error('Agent chat error:', err);
        this.agentMessages.push({ role: 'error', content: '连接失败: ' + err.message });
      } finally {
        this.agentLoading = false;
        this.scrollAgentToBottom();
      }
    },
  },
  mounted() {
    this.load();
    if (this.streamMode) this.startStream();
    else this.startAutoRefresh();
  },
  beforeUnmount() {
    this.stopAutoRefresh();
    this.stopStream();
  },
}).mount("#app");
