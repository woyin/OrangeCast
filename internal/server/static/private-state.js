(function(root){
 'use strict';
 function clear(){root.CWPListening?.clearPrivate();for(const storage of [root.localStorage,root.sessionStorage]){try{for(let i=storage.length-1;i>=0;i--){const key=storage.key(i);if(key?.startsWith('cwp-'))storage.removeItem(key);}}catch(_){}}}
 root.CWPPrivate={clear};
 // Covers redirects and direct visits to login, including logout from another tab.
 if(/^\/(login|register|logout)$/.test(root.location.pathname))clear();
})(window);
