  // ---- stock steppers (local nudge only; Save persists it) ----
  document.addEventListener('click', (e) => {
    const btn = e.target.closest('.stepper-btn');
    if (!btn) return;
    const input = btn.parentElement.querySelector('.stepper-input');
    let val = parseInt(input.value || '0', 10);
    val = btn.dataset.action === 'inc' ? val + 1 : Math.max(0, val - 1);
    input.value = val;
  });

  // ---- menu category tabs + search, combined (both must match) ----
  // #menuList gets wholesale-replaced both by its own initial hx-get and by
  // every /admin/menu/ws OOB push (any product/ingredient change anywhere
  // re-renders the whole list), so the filter is re-applied after every
  // swap rather than assumed to persist — same pattern as the guest menu's
  // category tabs.
  function applyMenuFilters(){
    const activeTab = document.querySelector('.menu-cat-tab.active');
    const cat = activeTab ? activeTab.dataset.cat : 'all';
    const searchEl = document.getElementById('menuSearch');
    const q = searchEl ? searchEl.value.trim().toLowerCase() : '';

    document.querySelectorAll('#menuList .menu-category').forEach(catEl => {
      const matchesCat = (cat === 'all' || catEl.dataset.category === cat);
      let visibleCount = 0;
      catEl.querySelectorAll('.product-row-wrap').forEach(wrap => {
        const name = wrap.querySelector('.product-name').textContent.toLowerCase();
        const show = matchesCat && (!q || name.includes(q));
        wrap.style.display = show ? '' : 'none';
        if (show) visibleCount++;
      });
      catEl.style.display = visibleCount > 0 ? '' : 'none';
    });
  }
  document.querySelectorAll('.menu-cat-tab').forEach(tab => {
    tab.addEventListener('click', () => {
      document.querySelectorAll('.menu-cat-tab').forEach(t => { t.classList.remove('active'); t.setAttribute('aria-selected', 'false'); });
      tab.classList.add('active');
      tab.setAttribute('aria-selected', 'true');
      applyMenuFilters();
    });
  });
  document.addEventListener('input', (e) => {
    if (e.target.id !== 'menuSearch') return;
    applyMenuFilters();
  });
  document.body.addEventListener('htmx:afterSwap', applyMenuFilters);
  document.body.addEventListener('htmx:oobAfterSwap', applyMenuFilters);

  // ---- same ∞/tracked toggle, per existing-product row (_product_list.html)
  // ----  delegated, not bound per-row, since every row gets wholesale
  // replaced on every menu re-render (Save, toggle, ingredient add/remove,
  // another admin's edit pushed over /admin/menu/ws — all of them). Checking
  // it disables the number input and both steppers so Save submits no
  // "stock" field at all, which parseStock (admin.go) reads as -1; unchecking
  // re-enables them starting from 0 so there's always something sane to bump.
  document.addEventListener('change', (e) => {
    if (!e.target.classList.contains('unlimited-toggle')) return;
    const wrap = e.target.closest('.stock-stepper');
    const input = wrap.querySelector('.stepper-input');
    input.disabled = e.target.checked;
    wrap.querySelectorAll('.stepper-btn').forEach(b => b.disabled = e.target.checked);
    if (e.target.checked) input.value = '';
    else if (!input.value) input.value = '0';
  });

  // ---- ingredient-add-form: price field only makes sense for "Extra" —
  // hidden for "Removable" (always free) and cleared so a stray leftover
  // value can't get submitted once it's hidden again.
  document.addEventListener('change', (e) => {
    if (!e.target.classList.contains('ingredient-kind-select')) return;
    const priceInput = e.target.closest('.ingredient-add-form').querySelector('.ingredient-price-input');
    const isExtra = e.target.value === 'extra';
    priceInput.style.display = isExtra ? '' : 'none';
    if (!isExtra) priceInput.value = '';
  });

  // ---- photo-field: two buttons sharing one hidden <input type=file>,
  // toggling its `capture` attribute so "Take photo" opens the phone's
  // camera directly while "Choose file" opens the normal picker/gallery —
  // same element either way, no need to merge two separate inputs into
  // one form field. Delegated from document since every product row's own
  // edit form gets this same control too (see _product_list.html), each
  // rendered/replaced live over /admin/menu/ws. ----
  document.addEventListener('click', (e) => {
    const takeBtn = e.target.closest('.photo-take-btn');
    const chooseBtn = e.target.closest('.photo-choose-btn');
    if (!takeBtn && !chooseBtn) return;
    const field = e.target.closest('.photo-field');
    const input = field.querySelector('.photo-file-input');
    if (takeBtn) input.setAttribute('capture', 'environment');
    else input.removeAttribute('capture');
    input.click();
  });
  // HEIC/HEIF (what an iPhone camera submits by default) can't be decoded
  // server-side here — see internal/imageproc's own doc comment on why
  // (short version: real decoding needs a libheif/libvips system
  // dependency this app's single-binary deploy story deliberately
  // avoids). The one place that conversion CAN happen is the one browser
  // that can actually decode a HEIC file at all: Safari, which is also
  // the only browser that ever produces one in the first place (Android
  // cameras are essentially always JPEG). So: detect it here, redraw it
  // through a <canvas> into a real JPEG, and swap that in before the file
  // ever leaves the browser. Anywhere else (a browser that can't decode
  // HEIC either), this quietly fails and the original file goes through
  // untouched — the server's own rejection message (imageproc.
  // ErrHEICNotSupported) still explains what to do about it.
  function looksLikeHEIC(file){
    return /\.hei[cf]$/i.test(file.name) || /^image\/hei[cf]/i.test(file.type || '');
  }
  function heicFileToJPEG(file){
    return new Promise((resolve, reject) => {
      const img = new Image();
      const url = URL.createObjectURL(file);
      img.onload = () => {
        const canvas = document.createElement('canvas');
        canvas.width = img.naturalWidth;
        canvas.height = img.naturalHeight;
        canvas.getContext('2d').drawImage(img, 0, 0);
        canvas.toBlob((blob) => {
          URL.revokeObjectURL(url);
          if (!blob) { reject(new Error('canvas produced no blob')); return; }
          resolve(new File([blob], file.name.replace(/\.hei[cf]$/i, '.jpg'), { type: 'image/jpeg' }));
        }, 'image/jpeg', 0.92);
      };
      img.onerror = () => { URL.revokeObjectURL(url); reject(new Error('this browser cannot decode HEIC')); };
      img.src = url;
    });
  }
  document.addEventListener('change', async (e) => {
    if (!e.target.classList.contains('photo-file-input')) return;
    const input = e.target;
    const field = input.closest('.photo-field');
    const preview = field.querySelector('.photo-preview-thumb');
    let file = input.files && input.files[0];
    if (!file) { preview.hidden = true; return; }

    if (looksLikeHEIC(file)) {
      try {
        file = await heicFileToJPEG(file);
        const dt = new DataTransfer();
        dt.items.add(file);
        input.files = dt.files;
      } catch (err) {
        // Can't convert here — let it through as-is; the server will
        // reject it with a clear, actionable message.
      }
    }

    const reader = new FileReader();
    reader.onload = () => { preview.src = reader.result; preview.hidden = false; };
    reader.readAsDataURL(file);
  });

