/**
 * Obsidian Sentinel WAF - Enterprise JavaScript Application
 * Core application logic, routing, and state management
 */

// ============================================
// AUTHENTICATION HELPER
// ============================================
function getStoredValue(key) {
    // Check localStorage first (primary), then sessionStorage (fallback)
    return localStorage.getItem(key) || sessionStorage.getItem(key);
}

function getStoredUser() {
    const userStr = getStoredValue('obsidian_user');
    try {
        return userStr ? JSON.parse(userStr) : null;
    } catch {
        return null;
    }
}

// ============================================
// APPLICATION STATE
// ============================================
const ObsidianApp = {
    token: getStoredValue('obsidian_token'),
    user: getStoredUser(),
    currentView: 'dashboard',
    ws: null,
    charts: {},
    refreshInterval: null,

    // Initialize the application
    init() {
        if (!this.token) {
            globalThis.location.href = '/login.html';
            return;
        }
        this.setupNavigation();
        this.setupUserProfile();
        this.connectWebSocket();
        this.loadView('dashboard');
        this.startAutoRefresh();
        this.setupEventListeners();
    },

    // Setup navigation event listeners
    setupNavigation() {
        document.querySelectorAll('[data-view]').forEach(el => {
            el.addEventListener('click', (e) => {
                e.preventDefault();
                this.loadView(el.dataset.view);
            });
        });

        // Hide admin features for non-admins
        if (this.user?.role !== 'Admin') {
            document.querySelectorAll('.admin-only').forEach(el => {
                el.style.display = 'none';
            });
        }
    },

    // Setup user profile display
    setupUserProfile() {
        const nameEl = document.getElementById('user-name');
        const roleEl = document.getElementById('user-role');
        if (nameEl) nameEl.innerText = this.user?.name || 'Admin';
        if (roleEl) roleEl.innerText = this.user?.role || 'Security Officer';
    },

    // Load a view/page
    async loadView(view) {
        this.currentView = view;

        // Update navigation state
        document.querySelectorAll('.nav-link').forEach(el => el.classList.remove('active'));
        const activeLink = document.querySelector(`[data-view="${view}"]`);
        if (activeLink) activeLink.classList.add('active');

        // Update page title
        const titles = {
            dashboard: 'Security Dashboard',
            logs: 'Attack Logs',
            rules: 'Rules Management',
            admin: 'Admin Panel',
            threats: 'Threat Intelligence',
            settings: 'Settings'
        };
        document.getElementById('pageTitle').innerText = titles[view] || 'Dashboard';

        // Toggle view visibility
        document.querySelectorAll('.view-section').forEach(el => el.classList.add('d-none'));
        const target = document.getElementById('view-' + view);
        if (target) target.classList.remove('d-none');

        // Load view-specific data
        switch (view) {
            case 'dashboard':
                await this.fetchDashboardData();
                break;
            case 'logs':
                await this.fetchLogs();
                break;
            case 'rules':
                await this.fetchRules();
                break;
            case 'admin':
                await this.fetchAdminData();
                break;
            case 'threats':
                await this.fetchThreatIntelligence();
                break;
        }
    },

    // API helper
    async api(endpoint, options = {}) {
        const headers = {
            'Authorization': 'Bearer ' + this.token,
            'Content-Type': 'application/json',
            ...options.headers
        };
        try {
            const res = await fetch(endpoint, { ...options, headers });
            if (res.status === 401) {
                this.logout();
                return null;
            }
            return await res.json();
        } catch (e) {
            console.error('API Error:', e);
            return null;
        }
    },

    // Fetch dashboard data
    async fetchDashboardData() {
        const [stats, logs, metrics, health] = await Promise.all([
            this.api('/api/stats'),
            this.api('/api/logs'),
            this.api('/api/metrics'),
            this.api('/api/health')
        ]);

        if (stats) {
            const totalThreats = (stats.blocked_requests || 0) + (stats.flagged_requests || 0);
            document.getElementById('total-attacks').innerText = totalThreats;
            document.getElementById('blocked-count').innerText = stats.blocked_requests || 0;
            document.getElementById('flagged-count').innerText = stats.flagged_requests || 0;
            document.getElementById('safe-requests').innerText = stats.safe_requests || 0;
            
            // Calculate and display dynamic delta (compare to stored previous value)
            this.updateThreatsDelta(totalThreats, logs || []);
            
            // Quick Stats - Real Data
            const quickRules = document.getElementById('quick-active-rules');
            if (quickRules) quickRules.innerText = stats.active_rules_count || 0;
            
            const quickTotal = document.getElementById('quick-total-requests');
            if (quickTotal) quickTotal.innerText = stats.total_requests || 0;

            // Pass logs to charts for real timeline data
            this.updateCharts(stats, logs || []);
        }

        if (metrics) {
            const uptimeEl = document.getElementById('quick-uptime');
            if (uptimeEl) {
                const secs = metrics.uptime_seconds || 0;
                const hours = Math.floor(secs / 3600);
                const mins = Math.floor((secs % 3600) / 60);
                uptimeEl.innerText = `${hours}h ${mins}m`;
            }
        }

        if (health) {
            const statusDot = document.getElementById('system-status-dot');
            const statusText = document.getElementById('system-status-text');
            if (health.status === 'healthy') {
                if (statusDot) statusDot.style.backgroundColor = '#10b981';
                if (statusText) statusText.innerText = 'System Operational';
            } else {
                if (statusDot) statusDot.style.backgroundColor = '#f43f5e';
                if (statusText) statusText.innerText = 'System Issue Detected';
            }
        }

        if (logs && logs.length > 0) {
            this.renderRecentLogs(logs.slice(-10).reverse());
        } else {
            const tbody = document.getElementById('logs-table');
            if (tbody) tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted py-4">No requests logged yet. Navigate around to see logs.</td></tr>';
        }
    },

    // Render recent logs with status classification
    renderRecentLogs(logs) {
        const tbody = document.getElementById('logs-table');
        if (!tbody) return;

        tbody.innerHTML = logs.map(log => {
            const date = new Date(log.timestamp).toLocaleTimeString();
            
            // Status badge based on log status
            let statusBadge = '';
            switch (log.status) {
                case 'Blocked':
                    statusBadge = '<span class="badge bg-danger">Blocked</span>';
                    break;
                case 'ThreatBlocked':
                    statusBadge = '<span class="badge bg-danger"><i class="fas fa-skull-crossbones me-1"></i>Threat</span>';
                    break;
                case 'Flagged':
                    statusBadge = '<span class="badge bg-warning text-dark">Flagged</span>';
                    break;
                case 'Safe':
                default:
                    statusBadge = '<span class="badge bg-success">Safe</span>';
                    break;
            }

            // Action badge
            const actionBadge = log.action === 'Pass' 
                ? '<span class="badge bg-dark border border-success text-success">Pass</span>'
                : `<span class="badge bg-dark border border-danger text-danger">${log.action || 'Unknown'}</span>`;

            // Calculate row class based on status
            let rowClass = '';
            if (log.status === 'Blocked' || log.status === 'ThreatBlocked') {
                rowClass = 'table-danger';
            } else if (log.status === 'Flagged') {
                rowClass = 'table-warning';
            }

            // Format URI display with truncation
            const uriDisplay = (log.uri || '/').substring(0, 30);
            const uriSuffix = (log.uri || '').length > 30 ? '...' : '';
            
            // Format details display with truncation
            const detailsDisplay = (log.details || '').substring(0, 40);
            const detailsSuffix = (log.details || '').length > 40 ? '...' : '';

            return `
                <tr class="${rowClass}">
                    <td class="text-secondary font-monospace small">${date}</td>
                    <td>${statusBadge}</td>
                    <td>${actionBadge}</td>
                    <td class="font-monospace small">${log.rule_id || '-'}</td>
                    <td><code class="text-info">${log.method || 'GET'} ${uriDisplay}${uriSuffix}</code></td>
                    <td class="text-white-50 small">${detailsDisplay}${detailsSuffix}</td>
                </tr>
            `;
        }).join('');
    },

    // Update charts
    updateCharts(stats, logs = []) {
        const ctx = document.getElementById('attackChart')?.getContext('2d');
        if (!ctx) return;

        // Calculate timeline data from logs (threats per hour for last 6 hours)
        const timelineData = this.calculateTimelineData(logs);

        if (this.charts.attack) {
            this.charts.attack.data.datasets[0].data = [
                stats.blocked_requests || 0,
                stats.flagged_requests || 0,
                stats.safe_requests || 0
            ];
            this.charts.attack.update();
        } else {
            this.charts.attack = new Chart(ctx, {
                type: 'doughnut',
                data: {
                    labels: ['Blocked', 'Flagged', 'Safe'],
                    datasets: [{
                        data: [
                            stats.blocked_requests || 0,
                            stats.flagged_requests || 0,
                            stats.safe_requests || 0
                        ],
                        backgroundColor: ['#f43f5e', '#fbbf24', '#10b981'],
                        borderWidth: 0
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: { legend: { position: 'bottom' } }
                }
            });
        }

        // Timeline chart
        const timelineCtx = document.getElementById('timelineChart')?.getContext('2d');
        if (timelineCtx) {
            if (this.charts.timeline) {
                // Update existing chart with real data
                this.charts.timeline.data.datasets[0].data = timelineData;
                this.charts.timeline.update();
            } else {
                this.charts.timeline = new Chart(timelineCtx, {
                    type: 'line',
                    data: {
                        labels: ['6h ago', '5h ago', '4h ago', '3h ago', '2h ago', '1h ago', 'Now'],
                        datasets: [{
                            label: 'Threats',
                            data: timelineData,
                            borderColor: '#f43f5e',
                            tension: 0.4,
                            fill: true,
                            backgroundColor: 'rgba(244, 63, 94, 0.1)'
                        }]
                    },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        plugins: { legend: { display: false } },
                        scales: {
                            y: { beginAtZero: true, grid: { color: '#3d424a' } },
                            x: { grid: { color: '#3d424a' } }
                        }
                    }
                });
            }
        }
    },

    // Calculate timeline data from logs (blocked requests per hour for last 6 hours)
    calculateTimelineData(logs) {
        const now = new Date();
        const hourBuckets = [0, 0, 0, 0, 0, 0, 0]; // 6h ago, 5h ago, ..., Now
        
        if (!logs || logs.length === 0) {
            return hourBuckets;
        }

        logs.forEach(log => {
            // Only count blocked requests
            if (log.status !== 'Blocked' && log.status !== 'ThreatBlocked') return;
            
            const logTime = new Date(log.timestamp);
            const hoursAgo = Math.floor((now - logTime) / (1000 * 60 * 60));
            
            if (hoursAgo < 0) return; // Future (shouldn't happen)
            if (hoursAgo > 6) return; // Older than 6 hours
            
            // Map to bucket: 0 hours ago = index 6 (Now), 6 hours ago = index 0
            const bucketIndex = 6 - hoursAgo;
            if (bucketIndex >= 0 && bucketIndex < 7) {
                hourBuckets[bucketIndex]++;
            }
        });

        return hourBuckets;
    },

    // Calculate and display threats delta (today vs last hour)
    updateThreatsDelta(currentTotal, logs) {
        const deltaEl = document.getElementById('threats-delta');
        if (!deltaEl) return;

        // Count threats from last hour vs this hour
        const now = new Date();
        const oneHourAgo = new Date(now - 60 * 60 * 1000);
        const twoHoursAgo = new Date(now - 2 * 60 * 60 * 1000);

        let thisHour = 0;
        let lastHour = 0;

        if (logs && logs.length > 0) {
            logs.forEach(log => {
                if (log.status !== 'Blocked' && log.status !== 'ThreatBlocked') return;
                const logTime = new Date(log.timestamp);
                if (logTime >= oneHourAgo) {
                    thisHour++;
                } else if (logTime >= twoHoursAgo) {
                    lastHour++;
                }
            });
        }

        // Calculate percentage change
        if (lastHour === 0 && thisHour === 0) {
            deltaEl.innerHTML = '<i class="fas fa-minus"></i> No recent activity';
            deltaEl.className = 'stat-delta text-muted';
        } else if (lastHour === 0) {
            deltaEl.innerHTML = `<i class="fas fa-arrow-up"></i> ${thisHour} new this hour`;
            deltaEl.className = 'stat-delta text-warning';
        } else {
            const changePercent = Math.round((thisHour - lastHour) / lastHour * 100);
            if (thisHour > lastHour) {
                deltaEl.innerHTML = `<i class="fas fa-arrow-up"></i> ${changePercent}% from last hour`;
                deltaEl.className = 'stat-delta text-danger';
            } else if (thisHour < lastHour) {
                deltaEl.innerHTML = `<i class="fas fa-arrow-down"></i> ${Math.abs(changePercent)}% from last hour`;
                deltaEl.className = 'stat-delta text-success';
            } else {
                deltaEl.innerHTML = '<i class="fas fa-equals"></i> Same as last hour';
                deltaEl.className = 'stat-delta text-muted';
            }
        }
    },

    // Fetch full logs
    async fetchLogs() {
        const logs = await this.api('/api/logs');
        const tbody = document.getElementById('full-logs-table');
        if (!tbody || !logs) return;

        if (logs.length === 0) {
            tbody.innerHTML = '<tr><td colspan="8" class="text-center text-muted py-4">No logs recorded yet. Navigate around to generate logs.</td></tr>';
            return;
        }

        // Reverse to show newest first
        const sortedLogs = [...logs].reverse();

        tbody.innerHTML = sortedLogs.map(log => {
            // Status badge
            let statusBadge = '';
            let rowClass = '';
            switch (log.status) {
                case 'Blocked':
                    statusBadge = '<span class="badge bg-danger">Blocked</span>';
                    rowClass = 'table-danger';
                    break;
                case 'ThreatBlocked':
                    statusBadge = '<span class="badge bg-danger"><i class="fas fa-skull me-1"></i>Threat</span>';
                    rowClass = 'table-danger';
                    break;
                case 'Flagged':
                    statusBadge = '<span class="badge bg-warning text-dark">Flagged</span>';
                    rowClass = 'table-warning';
                    break;
                case 'Safe':
                default:
                    statusBadge = '<span class="badge bg-success">Safe</span>';
                    break;
            }

            // Action badge
            const actionBadge = log.action === 'Pass' 
                ? '<span class="badge bg-success">Pass</span>'
                : `<span class="badge bg-danger">${log.action || 'Unknown'}</span>`;

            // Method badge color
            const methodColors = {
                'GET': 'bg-info',
                'POST': 'bg-warning text-dark',
                'PUT': 'bg-primary',
                'DELETE': 'bg-danger',
                'PATCH': 'bg-secondary'
            };
            const methodClass = methodColors[log.method] || 'bg-secondary';

            return `
                <tr class="${rowClass}">
                    <td class="text-secondary font-monospace small">${new Date(log.timestamp).toLocaleString()}</td>
                    <td>${statusBadge}</td>
                    <td>${log.client_ip || '127.0.0.1'}</td>
                    <td><span class="badge ${methodClass}">${log.method || 'GET'}</span></td>
                    <td class="font-monospace small"><code>${log.uri || '/'}</code></td>
                    <td>${log.rule_id || '-'}</td>
                    <td class="small text-muted">${(log.details || '').substring(0, 50)}${(log.details || '').length > 50 ? '...' : ''}</td>
                    <td>${actionBadge}</td>
                </tr>
            `;
        }).join('');

        // Update pagination text
        const paginationText = document.querySelector('.card-footer .text-muted');
        if (paginationText) {
            paginationText.textContent = `Showing ${Math.min(sortedLogs.length, 50)} of ${sortedLogs.length} entries`;
        }
    },

    // Fetch rules
    async fetchRules() {
        const rules = await this.api('/api/rules');
        const tbody = document.getElementById('rules-tbody');
        if (!tbody || !rules) return;

        tbody.innerHTML = rules.map(rule => {
            // Determine severity badge class
            let sevClass = 'badge-sev-medium';
            if (rule.severity === 'CRITICAL') {
                sevClass = 'badge-sev-critical';
            } else if (rule.severity === 'HIGH') {
                sevClass = 'badge-sev-high';
            }
            
            // Determine status badge - use positive condition
            const isEnabled = rule.enabled !== false;
            const statusBadge = isEnabled
                ? '<span class="badge bg-success">Active</span>'
                : '<span class="badge bg-secondary">Disabled</span>';

            return `
                <tr>
                    <td class="font-monospace">${rule.id}</td>
                    <td>${rule.description}</td>
                    <td><span class="badge ${sevClass}">${rule.severity || 'NOTICE'}</span></td>
                    <td>${rule.category || 'General'}</td>
                    <td>${statusBadge}</td>
                    <td>
                        <button class="btn btn-sm btn-outline-info me-1" onclick="ObsidianApp.editRule(${rule.id})" title="Edit Rule">
                            <i class="fas fa-edit"></i> Edit
                        </button>
                        <button class="btn btn-sm btn-outline-danger" onclick="ObsidianApp.deleteRule(${rule.id})" title="Delete Rule">
                            <i class="fas fa-trash"></i> Delete
                        </button>
                    </td>
                </tr>
            `;
        }).join('');
    },

    // Fetch admin data
    async fetchAdminData() {
        const [users, audit, metrics] = await Promise.all([
            this.api('/api/admin/users'),
            this.api('/api/admin/audit'),
            this.api('/api/metrics')
        ]);

        // Users table
        const usersTbody = document.getElementById('users-tbody');
        if (usersTbody && users) {
            usersTbody.innerHTML = users.map(u => `
                <tr>
                    <td>${u.username}</td>
                    <td><span class="badge bg-info">${u.role}</span></td>
                    <td>${u.enabled ? '<span class="badge bg-success">Active</span>' : '<span class="badge bg-danger">Disabled</span>'}</td>
                    <td><button class="btn btn-sm btn-outline-secondary" title="Edit User"><i class="fas fa-edit"></i> Edit</button></td>
                </tr>
            `).join('');
        }

        // Audit logs
        const auditList = document.getElementById('audit-list');
        if (auditList && audit) {
            auditList.innerHTML = audit.map(log => `
                <li class="list-group-item bg-transparent border-secondary text-light">
                    <small class="text-muted">${log.timestamp}</small><br>
                    <strong>${log.action}</strong> on ${log.resource}
                </li>
            `).join('');
        }

        // Metrics
        if (metrics) {
            document.getElementById('metric-memory').innerText = metrics.memory_alloc_mb ? metrics.memory_alloc_mb + ' MB' : '-';
            document.getElementById('metric-goroutines').innerText = metrics.goroutines || '-';
            document.getElementById('metric-rps').innerText = metrics.total_requests || '0';
            document.getElementById('metric-uptime').innerText = this.formatUptime(metrics.uptime_seconds);
        }
    },

    // Fetch threat intelligence
    async fetchThreatIntelligence() {
        const threats = await this.api('/api/threats');
        const tbody = document.getElementById('threats-tbody');
        if (!tbody) return;

        if (threats && threats.length > 0) {
            tbody.innerHTML = threats.map(t => {
                // Determine risk level badge color
                let riskColor = 'secondary';
                if (t.risk_level === 'HIGH') {
                    riskColor = 'danger';
                } else if (t.risk_level === 'MEDIUM') {
                    riskColor = 'warning';
                }
                
                return `
                    <tr>
                        <td class="font-monospace">${t.ip}</td>
                        <td><span class="badge bg-${riskColor}">${t.risk_level}</span></td>
                        <td>${t.category}</td>
                        <td>${t.source}</td>
                        <td>${new Date(t.last_seen).toLocaleString()}</td>
                        <td><button class="btn btn-sm btn-outline-danger" onclick="ObsidianApp.blockIP('${t.ip}')" title="Block IP"><i class="fas fa-ban"></i> Block</button></td>
                    </tr>
                `;
            }).join('');
        } else {
            tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted">No threat intelligence data available</td></tr>';
        }
    },

    // Rule CRUD operations
    openRuleModal(ruleId = null) {
        const modal = new bootstrap.Modal(document.getElementById('ruleModal'));
        document.getElementById('ruleModalTitle').innerText = ruleId ? 'Edit Rule' : 'Add New Rule';
        document.getElementById('ruleEditId').value = ruleId || '';
        document.getElementById('ruleId').value = '';
        document.getElementById('ruleDesc').value = '';
        document.getElementById('ruleCategory').value = 'XSS';
        document.getElementById('ruleSeverity').value = 'CRITICAL';
        document.getElementById('ruleEnabled').checked = true;
        document.getElementById('ruleId').disabled = !!ruleId;

        if (ruleId) {
            this.loadRuleForEdit(ruleId);
        }
        modal.show();
    },

    async loadRuleForEdit(ruleId) {
        const rules = await this.api('/api/rules');
        const rule = rules?.find(r => r.id === ruleId);
        if (rule) {
            document.getElementById('ruleId').value = rule.id;
            document.getElementById('ruleDesc').value = rule.description;
            document.getElementById('ruleCategory').value = rule.category || 'XSS';
            document.getElementById('ruleSeverity').value = rule.severity || 'CRITICAL';
            document.getElementById('ruleEnabled').checked = rule.enabled !== false;
        }
    },

    async saveRule() {
        const editId = document.getElementById('ruleEditId').value;
        const rule = {
            id: Number.parseInt(document.getElementById('ruleId').value, 10),
            description: document.getElementById('ruleDesc').value,
            category: document.getElementById('ruleCategory').value,
            severity: document.getElementById('ruleSeverity').value,
            enabled: document.getElementById('ruleEnabled').checked
        };

        const endpoint = editId ? '/api/rules/update' : '/api/rules/create';
        const method = editId ? 'PUT' : 'POST';

        const data = await this.api(endpoint, { method, body: JSON.stringify(rule) });
        if (data?.success) {
            this.showToast('Success', editId ? 'Rule updated' : 'Rule created');
            bootstrap.Modal.getInstance(document.getElementById('ruleModal')).hide();
            this.fetchRules();
        } else {
            this.showToast('Error', data?.message || 'Failed to save rule', true);
        }
    },

    editRule(ruleId) {
        this.openRuleModal(ruleId);
    },

    async deleteRule(ruleId) {
        if (!confirm('Delete rule ' + ruleId + '?')) return;

        const data = await this.api('/api/rules/delete', {
            method: 'DELETE',
            body: JSON.stringify({ id: ruleId })
        });

        if (data?.success) {
            this.showToast('Success', 'Rule deleted');
            this.fetchRules();
        } else {
            this.showToast('Error', data?.message || 'Failed to delete', true);
        }
    },

    async blockIP(ip) {
        if (!confirm(`Block IP ${ip}?`)) return;
        const data = await this.api('/api/threats/block', {
            method: 'POST',
            body: JSON.stringify({ ip })
        });
        this.showToast(data?.success ? 'Success' : 'Error', data?.message || 'IP blocked');
    },

    // WebSocket connection
    connectWebSocket() {
        const isSecure = globalThis.location.protocol === 'https:';
        const protocol = isSecure ? 'wss:' : 'ws:';
        this.ws = new WebSocket(`${protocol}//${globalThis.location.host}/api/ws?token=${this.token}`);

        this.ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                if (msg.type === 'stats_update' && this.currentView === 'dashboard') {
                    this.fetchDashboardData();
                } else if (msg.type === 'alert') {
                    this.showToast('⚠️ Attack Blocked!', msg.data?.details || 'Security Event Detected', true);
                }
            } catch (parseError) {
                console.error('WebSocket message parse error:', parseError.message);
            }
        };

        this.ws.onclose = () => {
            setTimeout(() => this.connectWebSocket(), 2000);
        };
    },

    // Auto refresh
    startAutoRefresh() {
        this.refreshInterval = setInterval(() => {
            if (this.currentView === 'dashboard') {
                this.fetchDashboardData();
            }
        }, 30000);
    },

    // Setup event listeners
    setupEventListeners() {
        // Search filter
        document.getElementById('logs-search')?.addEventListener('keyup', function () {
            const filter = this.value.toLowerCase();
            document.querySelectorAll('#full-logs-table tr').forEach(row => {
                row.style.display = row.innerText.toLowerCase().includes(filter) ? '' : 'none';
            });
        });

        // Export button
        document.getElementById('downloadReport')?.addEventListener('click', (e) => {
            e.preventDefault();
            this.exportReport();
        });

        // Mobile toggle
        const sidebar = document.getElementById('sidebar');
        const overlay = document.querySelector('.overlay');
        
        document.querySelector('.mobile-toggle')?.addEventListener('click', () => {
            sidebar?.classList.toggle('active');
            overlay?.classList.toggle('active');
        });

        // Close sidebar when clicking overlay
        overlay?.addEventListener('click', () => {
            sidebar?.classList.remove('active');
            overlay?.classList.remove('active');
        });

        // Close sidebar when clicking a nav link on mobile
        document.querySelectorAll('.nav-link').forEach(link => {
            link.addEventListener('click', () => {
                if (window.innerWidth <= 992) {
                    sidebar?.classList.remove('active');
                    overlay?.classList.remove('active');
                }
            });
        });
    },

    // Export report
    async exportReport() {
        try {
            const res = await fetch('/api/export', {
                headers: { 'Authorization': 'Bearer ' + this.token }
            });
            const blob = await res.blob();
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'obsidian-report.pdf';
            a.click();
            URL.revokeObjectURL(url);
        } catch (exportError) {
            console.error('Export failed:', exportError.message);
            this.showToast('Error', 'Failed to export report', true);
        }
    },

    // Toast notification
    showToast(title, message, isError = false) {
        const container = document.getElementById('toast-container');
        const id = 'toast-' + Date.now();
        const color = isError ? 'text-bg-danger' : 'text-bg-dark border-secondary text-white';

        container.innerHTML += `
            <div id="${id}" class="toast align-items-center ${color} border-0 mb-2 show" role="alert">
                <div class="d-flex">
                    <div class="toast-body"><strong>${title}</strong><br>${message}</div>
                    <button type="button" class="btn-close btn-close-white me-2 m-auto" onclick="this.parentElement.parentElement.remove()"></button>
                </div>
            </div>
        `;

        setTimeout(() => document.getElementById(id)?.remove(), 5000);
    },

    // Format uptime
    formatUptime(seconds) {
        if (!seconds) return '-';
        const h = Math.floor(seconds / 3600);
        const m = Math.floor((seconds % 3600) / 60);
        return `${h}h ${m}m`;
    },

    // Logout
    logout() {
        localStorage.removeItem('obsidian_token');
        localStorage.removeItem('obsidian_user');
        globalThis.location.href = '/login.html';
    }
};

// Initialize on load
document.addEventListener('DOMContentLoaded', () => ObsidianApp.init());

// Global functions for inline handlers
function loadView(view) { ObsidianApp.loadView(view); }
function logout() { ObsidianApp.logout(); }
function openRuleModal(id) { ObsidianApp.openRuleModal(id); }
function saveRule() { ObsidianApp.saveRule(); }
function deleteRule(id) { ObsidianApp.deleteRule(id); }
