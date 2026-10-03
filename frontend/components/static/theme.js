// Тема консоли. Файл подключается в <head> синхронно (см. BootstrapScripts в
// components.Layout): верхняя часть выполняется до первой отрисовки страницы и
// ставит тему без вспышки. Привязка переключателя — после готовности DOM.
//
// Режимы: system (следует за ОС), light, dark. Выбранный режим хранится в
// localStorage; в атрибут data-theme на <html> всегда пишется разрешённая тема
// (light/dark), поэтому в console.css достаточно одной тёмной темы. Режим
// system разрешается через matchMedia и слушает смену системной схемы.
(function () {
  var root = document.documentElement;
  var MODES = ['system', 'light', 'dark'];
  var KEY = 'console-theme';
  var mql = window.matchMedia('(prefers-color-scheme: dark)');

  function readMode() {
    try {
      var v = localStorage.getItem(KEY);
      return MODES.indexOf(v) >= 0 ? v : 'system';
    } catch (e) {
      return 'system';
    }
  }

  function resolve(mode) {
    if (mode === 'light' || mode === 'dark') return mode;
    return mql.matches ? 'dark' : 'light';
  }

  function apply(mode) {
    var theme = resolve(mode);
    root.setAttribute('data-theme', theme);
    root.setAttribute('data-mode', mode);
    root.style.colorScheme = theme;
  }

  function label(mode) {
    if (mode === 'light') return 'Светлая';
    if (mode === 'dark') return 'Тёмная';
    return 'Как в системе';
  }

  // Синхронно, до отрисовки: применяем сохранённый режим.
  apply(readMode());

  // Смена системной схемы влияет только на режим system.
  var onSystemChange = function () {
    if (readMode() === 'system') apply('system');
  };
  if (mql.addEventListener) mql.addEventListener('change', onSystemChange);
  else if (mql.addListener) mql.addListener(onSystemChange);

  window.addEventListener('DOMContentLoaded', function () {
    var toggle = document.getElementById('theme-toggle');
    if (!toggle) return;

    var mode = readMode();

    function paint() {
      toggle.setAttribute('data-mode', mode);
      toggle.setAttribute('aria-label', 'Тема оформления: ' + label(mode) + '. Нажмите, чтобы сменить.');
      var value = toggle.querySelector('[data-theme-name]');
      if (value) value.textContent = label(mode);
    }

    toggle.addEventListener('click', function () {
      mode = MODES[(MODES.indexOf(mode) + 1) % MODES.length];
      try {
        if (mode === 'system') localStorage.removeItem(KEY);
        else localStorage.setItem(KEY, mode);
      } catch (e) {
        // Приватный режим или запрет хранилища: тема применится на текущей странице.
      }
      apply(mode);
      paint();
    });

    paint();
  });
})();
