// portal.js — живое обновление дерева заявок на главном экране портала.
//
// Подписывается на SSE-поток /portal/stream и перерисовывает список заявок без
// перезагрузки страницы. Подписи статусов и сообщения повторяют каталог сообщений
// портала (portal.request.status.*, portal.requests.*): клиентский слой не тянет
// серверный каталог, поэтому дублирует закрытый набор подписей.
(function () {
  "use strict";

  // adminBasePath возвращает внешний префикс пути портала под единым origin
  // (admin-proxy). Префикс вставляет сервер в <meta name="admin-base-path">,
  // чтобы адрес SSE-потока не дублировался в скрипте.
  function adminBasePath() {
    var meta = document.querySelector('meta[name="admin-base-path"]');
    return meta && meta.content ? meta.content : '';
  }

  var tree = document.getElementById("request-tree");
  if (!tree) {
    return;
  }

  var statusLabels = {
    "submitted": "Принята",
    "validating": "На проверке",
    "validated": "Проверена",
    "processing": "В работе",
    "fulfilled": "Выполнена",
    "rejected": "Отклонена",
    "cancelled": "Отменена",
    "needs-revision": "На доработке",
    "failed": "Сбой"
  };

  var messages = {
    unavailable: "История заявок недоступна.",
    empty: "Заявок пока нет."
  };

  function statusLabel(status) {
    return statusLabels[status] || status;
  }

  function render(data) {
    if (data.unavailable) {
      tree.innerHTML = '<p class="portal-empty" data-request-state="unavailable">' + messages.unavailable + "</p>";
      return;
    }
    var requests = data.requests || [];
    if (requests.length === 0) {
      tree.innerHTML = '<p class="portal-empty" data-request-state="empty">' + messages.empty + "</p>";
      return;
    }
    var html = '<ul class="request-tree-list">';
    requests.forEach(function (request) {
      html += '<li class="request-tree-node" role="treeitem" data-request-id="' + request.request_id + '">'
        + '<span class="request-tree-id">' + request.request_id + "</span>"
        + '<span class="status status-' + request.status + '">' + statusLabel(request.status) + "</span>"
        + "</li>";
    });
    html += "</ul>";
    tree.innerHTML = html;
  }

  var source = new EventSource(adminBasePath() + "/portal/stream");
  source.addEventListener("requests", function (event) {
    render(JSON.parse(event.data));
  });
})();
