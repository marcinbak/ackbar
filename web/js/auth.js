// Authentication & Fetch Interceptor
function getAuthToken() {
  const urlParams = new URLSearchParams(window.location.search);
  const urlToken = urlParams.get('token');
  if (urlToken) {
    localStorage.setItem('ackbar_token', urlToken);
    return urlToken;
  }
  return localStorage.getItem('ackbar_token') || '';
}

function setAuthToken(token) {
  if (token) {
    localStorage.setItem('ackbar_token', token);
  } else {
    localStorage.removeItem('ackbar_token');
  }
}

// Intercept window.fetch to automatically inject auth headers and handle 401
const originalFetch = window.fetch;
window.fetch = function(input, init = {}) {
  const token = getAuthToken();
  if (token) {
    init.headers = init.headers || {};
    if (init.headers instanceof Headers) {
      if (!init.headers.has('Authorization')) init.headers.set('Authorization', `Bearer ${token}`);
      if (!init.headers.has('X-Ackbar-Token')) init.headers.set('X-Ackbar-Token', token);
    } else if (Array.isArray(init.headers)) {
      init.headers.push(['Authorization', `Bearer ${token}`]);
      init.headers.push(['X-Ackbar-Token', token]);
    } else {
      init.headers['Authorization'] = init.headers['Authorization'] || `Bearer ${token}`;
      init.headers['X-Ackbar-Token'] = init.headers['X-Ackbar-Token'] || token;
    }
  }
  return originalFetch(input, init).then(res => {
    if (res.status === 401 && !window.__promptingToken) {
      window.__promptingToken = true;
      const promptToken = prompt('Ackbar Daemon requires an API Authentication Token:');
      window.__promptingToken = false;
      if (promptToken) {
        setAuthToken(promptToken.trim());
        location.reload();
      }
    }
    return res;
  });
};

export {
  getAuthToken,
  setAuthToken
};
