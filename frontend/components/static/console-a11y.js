// Управление фокусом при подмене фрагментов HTMX и живые объявления
// (web-frontend-architecture, срез 7, 7.3; design.md, Решение 13).
//
// При подмене фрагмента фокус теряется, если заменён элемент, на котором он
// стоял. Скрипт возвращает фокус по явному указанию разметки: элемент, который
// HTMX заменил, несёт атрибут
//   data-focus-after-swap — CSS-селектор элемента, который должен получить фокус
//                            после подмены (например, заголовок списка или поле);
//   data-live-announce     — текст объявления для области aria-live.
// Если указаний нет, скрипт ничего не делает и браузер сохраняет фокус.
//
// Файл не зависит от HTMX: без него он только слушает события документа и не
// мешает обычной навигации.
(function () {
  function announce(text) {
    if (!text) return;
    var region = document.querySelector('[data-live-region]');
    if (!region) return;
    // Пустая→непустая установка заставляет ассистента прочитать объявление
    // повторно, даже если текст совпал с предыдущим.
    region.textContent = '';
    window.setTimeout(function () {
      region.textContent = text;
    }, 0);
  }

  function focusTarget(root, selector) {
    if (!selector) return null;
    if (root && root.matches && root.matches(selector)) return root;
    var scope = root && root.querySelector ? root : document;
    var found = scope.querySelector(selector);
    // Цель фокуса может быть устойчивым элементом вне подменённого узла
    // (заголовок списка, поле формы над фрагментом) — тогда ищем по документу.
    if (!found && scope !== document) found = document.querySelector(selector);
    return found;
  }

  document.addEventListener('htmx:afterSwap', function (event) {
    var target = event && event.target ? event.target : document.body;
    if (!target || !target.getAttribute) return;

    announce(target.getAttribute('data-live-announce'));

    var focus = focusTarget(target, target.getAttribute('data-focus-after-swap'));
    if (focus && typeof focus.focus === 'function') focus.focus();
  });
})();
