function extractDomain(url) {
  try {
    const urlObj = new URL(url);
    return urlObj.hostname.replace("www.", "");
  } catch (e) {
    return "";
  }
}

function renderAvatarStack(sharers, maxVisible = 5) {
  if (!sharers || sharers.length === 0) {
    return "";
  }

  const visibleSharers = sharers.slice(0, maxVisible);
  const remainingCount = sharers.length - maxVisible;

  let html = '<div class="avatar-stack">';
  html += '<span class="avatar-label">Shared by:</span>';
  html += '<div class="avatar-list">';

  visibleSharers.forEach((sharer) => {
    const displayName = sharer.display_name || sharer.handle;
    const avatarUrl = sharer.avatar_url || "/static/img/default-avatar.svg";

    html += `<img
            src="${avatarUrl}"
            alt="${displayName}"
            title="${displayName} (@${sharer.handle})"
            class="avatar"
        />`;
  });

  if (remainingCount > 0) {
    html += `<div class="avatar-more" title="${remainingCount} more">+${remainingCount}</div>`;
  }

  html += "</div>";
  html += "</div>";

  return html;
}

function loadTrending() {
  const hours = document.getElementById("hours").value;
  const limit = document.getElementById("limit").value;
  const container = document.getElementById("links");

  container.innerHTML = '<div class="loading">Loading trending links...</div>';

  fetch(`/api/trending?hours=${hours}&limit=${limit}&degree=2`)
    .then((res) => {
      if (!res.ok) throw new Error("Failed to fetch trending links");
      return res.json();
    })
    .then((data) => {
      if (!data.links || data.links.length === 0) {
        container.innerHTML =
          '<div class="loading">No trending links found. The poller may still be collecting data.</div>';
        return;
      }

      container.innerHTML = "";
      data.links.forEach((link) => {
        const card = document.createElement("div");
        card.className = "link-card";

        const domain = extractDomain(link.url);

        card.innerHTML = `
                    ${
                      link.image_url
                        ? `
                        <div class="link-image">
                            <img src="${link.image_url}" alt="${
                            link.title || "Link preview"
                          }" onerror="this.parentElement.style.display='none'">
                        </div>
                    `
                        : ""
                    }
                    <div class="link-content">
                        <h3><a href="${
                          link.url
                        }" target="_blank" rel="noopener noreferrer">${
          link.title || link.url
        }</a></h3>
                        ${
                          domain
                            ? `<div class="link-domain">${domain}</div>`
                            : ""
                        }
                        ${
                          link.description
                            ? `<p class="link-description">${link.description}</p>`
                            : ""
                        }
                        <div class="link-meta">
                            <span class="share-count">★ ${
                              link.share_count
                            } share${link.share_count !== 1 ? "s" : ""}</span>
                        </div>
                        ${renderAvatarStack(link.sharer_avatars)}
                        <button class="posts-toggle" data-link-id="${
                          link.id
                        }">Show Posts ▼</button>
                        <div class="posts-container" id="posts-${link.id}"></div>
                    </div>
                `;

        container.appendChild(card);
      });
    })
    .catch((err) => {
      container.innerHTML = `<div class="error">Error: ${err.message}</div>`;
    });
}

function togglePosts(button, linkId) {
  const container = document.getElementById(`posts-${linkId}`);

  if (container.classList.contains("expanded")) {
    container.classList.remove("expanded");
    button.textContent = "Show Posts ▼";
  } else {
    container.classList.add("expanded");
    button.textContent = "Hide Posts ▲";

    // Load posts if not already loaded
    if (!container.dataset.loaded) {
      loadPosts(linkId, container);
    }
  }
}

function loadPosts(linkId, container) {
  container.innerHTML = '<div class="loading">Loading posts...</div>';

  fetch(`/api/links/${linkId}/posts`)
    .then((res) => {
      if (!res.ok) throw new Error("Failed to fetch posts");
      return res.json();
    })
    .then((data) => {
      container.dataset.loaded = "true";
      renderPosts(data.posts, container);
    })
    .catch((err) => {
      container.innerHTML = `<div class="error">Error loading posts: ${err.message}</div>`;
    });
}

function renderPosts(posts, container) {
  if (!posts || posts.length === 0) {
    container.innerHTML = '<div class="loading">No posts found for this link.</div>';
    return;
  }

  let html = '<div class="posts-list">';
  posts.forEach((post) => {
    const displayName = post.display_name || post.handle;
    const avatarUrl = post.avatar_url || "/static/img/default-avatar.svg";
    const postDate = new Date(post.created_at).toLocaleDateString("en-US", {
      month: "short",
      day: "numeric",
      year: "numeric",
    });

    // Extract rkey from post ID (format: at://did/app.bsky.feed.post/rkey)
    const rkey = post.id.split('/').pop();
    const postUrl = `https://bsky.app/profile/${post.handle}/post/${rkey}`;
    const profileUrl = `https://bsky.app/profile/${post.handle}`;

    html += `
      <div class="post-item">
        <div class="post-author">
          <img
            src="${avatarUrl}"
            alt="${displayName}"
            class="post-avatar"
          />
          <div class="post-author-info">
            <a href="${profileUrl}" target="_blank" rel="noopener noreferrer" class="post-author-name">${displayName}</a>
            <a href="${profileUrl}" target="_blank" rel="noopener noreferrer" class="post-author-handle">@${post.handle}</a>
          </div>
          <a href="${postUrl}" target="_blank" rel="noopener noreferrer" class="post-date">${postDate}</a>
        </div>
        <div class="post-content">${escapeHtml(post.content)}</div>
      </div>
    `;
  });
  html += "</div>";

  container.innerHTML = html;
}

function escapeHtml(text) {
  const div = document.createElement("div");
  div.textContent = text;
  return div.innerHTML;
}

// Initialize when DOM is ready
document.addEventListener("DOMContentLoaded", () => {
  // Load trending links on page load
  loadTrending();

  // Refresh button click handler
  document.getElementById("refresh-btn").addEventListener("click", loadTrending);

  // Allow Enter key to refresh
  document.addEventListener("keypress", (e) => {
    if (e.key === "Enter") loadTrending();
  });

  // Event delegation for post toggle buttons
  document.addEventListener("click", (e) => {
    if (e.target.classList.contains("posts-toggle")) {
      const linkId = e.target.dataset.linkId;
      togglePosts(e.target, linkId);
    }
  });

  // Event delegation for image error handling (avatar fallbacks)
  document.addEventListener(
    "error",
    (e) => {
      if (e.target.tagName === "IMG" && (e.target.classList.contains("avatar") || e.target.classList.contains("post-avatar"))) {
        e.target.src = "/static/img/default-avatar.svg";
      }
    },
    true
  );
});
