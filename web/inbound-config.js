// 入站配置的纯数据构造逻辑。页面层只负责读取 DOM 和展示校验错误。
window.NodePilotInboundConfig = (() => {
  function parse(raw) {
    try { return raw ? JSON.parse(raw) : {}; } catch (e) { return {}; }
  }

  function advanced(input) {
    const trans = input.transport;
    let stream = {};
    if (trans === 'ws') {
      stream = { wsSettings: { path: input.wsPath || '/v2ray' } };
      if (input.wsHost) stream.wsSettings.headers = { Host: input.wsHost };
    }
    if (trans === 'tcp' && input.tcpObfs === 'http') {
      const request = {
        version: '1.1', method: 'GET',
        path: [input.tcpPath || '/'],
        headers: { Connection: ['keep-alive'] }
      };
      if (input.tcpHost) request.headers.Host = [input.tcpHost];
      stream.tcpSettings = { header: { type: 'http', request } };
    }
    if (trans === 'grpc') {
      stream.grpcSettings = { serviceName: input.grpcName || 'xray' };
      if (input.grpcMulti) stream.grpcSettings.mode = 'multi';
    }
    return Object.keys(stream).length ? JSON.stringify(stream) : '';
  }

  return { parse, advanced };
})();
