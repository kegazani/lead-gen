// Живое обновление статусов заявок компании (SSE).
//
// Скрипт подключается экраном /company и открывает поток /company/stream для
// активного тенанта. На каждый кадр со списком [{request_id, status}] он
// обновляет подписи и классы строк статусов без перезагрузки страницы. Подписи
// статусов приходят из разметки (data-status-labels), а не дублируются здесь:
// единственный источник пользовательского текста — каталог сообщений сервера.
(function () {
  var section = document.querySelector('.company-requests');
  if (!section) {
    return;
  }
  // adminBasePath возвращает внешний префикс пути консоли под единым origin
  // (admin-proxy): адрес SSE-потока не дублируется в скрипте.
  function adminBasePath() {
    var meta = document.querySelector('meta[name="admin-base-path"]');
    return meta && meta.content ? meta.content : '';
  }
  var tenant = section.getAttribute('data-tenant') || '';
  var statusLabels = {};
  try {
    statusLabels = JSON.parse(section.getAttribute('data-status-labels') || '{}');
  } catch (e) {
    statusLabels = {};
  }
  if (!tenant) {
    return;
  }

  var source = new EventSource(adminBasePath() + '/company/stream?tenant_id=' + encodeURIComponent(tenant));
  source.addEventListener('statuses', function (event) {
    var rows;
    try {
      rows = JSON.parse(event.data);
    } catch (e) {
      return;
    }
    rows.forEach(function (row) {
      var node = section.querySelector('[data-request-id="' + row.request_id + '"]');
      if (!node) {
        return;
      }
      node.className = 'status status-' + row.status;
      node.textContent = statusLabels[row.status] || row.status;
    });
  });
})();
