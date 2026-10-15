// 入站页面的纯展示与缓存查询辅助函数。
window.NodePilotInboundUI = (() => {
  function label(inbound) {
    return inbound.name || ((inbound.protocol || '?') + ':' + (inbound.port || '?'));
  }

  function nodeId(inboundId, cache, fallback) {
    for (const nid in (cache || {})) {
      const arr = cache[nid] || [];
      if (arr.some(x => Number(x.id) === Number(inboundId))) return Number(nid);
    }
    return fallback || null;
  }

  return { label, nodeId };
})();
