/**
 * Android UI Progress Module
 * 显示 VPN 启动和网络操作进度
 */

// 进度状态元素
let progressBar = null;
let progressText = null;

/**
 * 初始化进度 UI
 */
function initProgressUI() {
  // 创建进度条容器
  const container = document.createElement('div');
  container.id = 'snet-progress-overlay';
  container.style.cssText = `
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background: rgba(0, 0, 0, 0.5);
    display: none;
    align-items: center;
    justify-content: center;
    z-index: 10000;
  `;

  // 进度框
  const box = document.createElement('div');
  box.style.cssText = `
    background: var(--card, #fff);
    border-radius: 12px;
    padding: 24px 32px;
    max-width: 300px;
    text-align: center;
  `;

  // 进度文本
  progressText = document.createElement('div');
  progressText.id = 'snet-progress-text';
  progressText.style.cssText = `
    font-size: 16px;
    color: var(--foreground, #333);
    margin-bottom: 16px;
  `;
  progressText.textContent = '正在初始化...';

  // 进度条
  progressBar = document.createElement('div');
  progressBar.style.cssText = `
    width: 100%;
    height: 4px;
    background: var(--border, #e0e0e0);
    border-radius: 2px;
    overflow: hidden;
  `;

  const barFill = document.createElement('div');
  barFill.id = 'snet-progress-fill';
  barFill.style.cssText = `
    width: 0%;
    height: 100%;
    background: var(--accent, #4CAF50);
    transition: width 0.3s;
  `;
  progressBar.appendChild(barFill);

  box.appendChild(progressText);
  box.appendChild(progressBar);
  container.appendChild(box);
  document.body.appendChild(container);
}

/**
 * 显示进度
 */
window.snetProgress = function(status) {
  if (!progressBar) {
    initProgressUI();
  }

  const overlay = document.getElementById('snet-progress-overlay');
  const text = document.getElementById('snet-progress-text');
  const fill = document.getElementById('snet-progress-fill');

  if (!overlay) return;

  // 显示进度
  overlay.style.display = 'flex';

  // 更新文本
  if (text && status) {
    text.textContent = status;
  }

  // 动画进度条
  if (fill) {
    const current = parseInt(fill.style.width) || 0;
    const next = Math.min(current + 20, 90);
    fill.style.width = next + '%';
  }
};

/**
 * 隐藏进度
 */
window.snetProgressDone = function(success = true) {
  const overlay = document.getElementById('snet-progress-overlay');
  const fill = document.getElementById('snet-progress-fill');

  if (overlay) {
    // 完成进度条
    if (fill) {
      fill.style.width = '100%';
    }

    // 延迟隐藏
    setTimeout(() => {
      overlay.style.display = 'none';
      // 重置进度条
      if (fill) {
        fill.style.width = '0%';
      }
    }, success ? 500 : 1000);
  }
};

// 自动初始化
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initProgressUI);
} else {
  initProgressUI();
}

console.log('[SNET Progress UI] Loaded');