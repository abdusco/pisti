(() => {
  try {
    const theme = localStorage.getItem('pisti.theme') || 'system';
    document.documentElement.dataset.theme = theme === 'system' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : theme;
    document.documentElement.lang = localStorage.getItem('pisti.language') || (navigator.language.startsWith('tr') ? 'tr' : 'en');
  } catch { document.documentElement.dataset.theme = 'light'; }
})();
