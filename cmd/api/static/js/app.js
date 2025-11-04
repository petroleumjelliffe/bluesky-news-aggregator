function extractDomain(url) {
    try {
        const urlObj = new URL(url);
        return urlObj.hostname.replace('www.', '');
    } catch (e) {
        return '';
    }
}

function loadTrending() {
    const hours = document.getElementById('hours').value;
    const limit = document.getElementById('limit').value;
    const container = document.getElementById('links');

    container.innerHTML = '<div class="loading">Loading trending links...</div>';

    fetch(`/api/trending?hours=${hours}&limit=${limit}`)
        .then(res => {
            if (!res.ok) throw new Error('Failed to fetch trending links');
            return res.json();
        })
        .then(data => {
            if (!data.links || data.links.length === 0) {
                container.innerHTML = '<div class="loading">No trending links found. The poller may still be collecting data.</div>';
                return;
            }

            container.innerHTML = '';
            data.links.forEach(link => {
                const card = document.createElement('div');
                card.className = 'link-card';

                const domain = extractDomain(link.url);

                card.innerHTML = `
                    ${link.image_url ? `
                        <div class="link-image">
                            <img src="${link.image_url}" alt="${link.title || 'Link preview'}" onerror="this.parentElement.style.display='none'">
                        </div>
                    ` : ''}
                    <div class="link-content">
                        <h3><a href="${link.url}" target="_blank" rel="noopener noreferrer">${link.title || link.url}</a></h3>
                        ${domain ? `<div class="link-domain">${domain}</div>` : ''}
                        ${link.description ? `<p class="link-description">${link.description}</p>` : ''}
                        <div class="link-meta">
                            <span class="share-count">★ ${link.share_count} share${link.share_count !== 1 ? 's' : ''}</span>
                            <span class="sharers">Shared by: ${link.sharers.slice(0, 3).join(', ')}${link.sharers.length > 3 ? ` and ${link.sharers.length - 3} more` : ''}</span>
                        </div>
                        <button class="posts-toggle" onclick="togglePosts(this, ${link.id})">Show Posts ▼</button>
                        <div class="posts-container" id="posts-${link.id}">
                            <div class="loading">Posts will be loaded here in Phase 3...</div>
                        </div>
                    </div>
                `;

                container.appendChild(card);
            });
        })
        .catch(err => {
            container.innerHTML = `<div class="error">Error: ${err.message}</div>`;
        });
}

function togglePosts(button, linkId) {
    const container = document.getElementById(`posts-${linkId}`);

    if (container.classList.contains('expanded')) {
        container.classList.remove('expanded');
        button.textContent = 'Show Posts ▼';
    } else {
        container.classList.add('expanded');
        button.textContent = 'Hide Posts ▲';
    }
}

// Load on page load
loadTrending();

// Allow Enter key to refresh
document.addEventListener('keypress', (e) => {
    if (e.key === 'Enter') loadTrending();
});
