// Настройка темы
const systemTheme = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
if(!['dark', 'light'].includes(localStorage?.theme)) localStorage.theme = systemTheme;
document.documentElement.dataset.theme = localStorage.theme;

const toggle = () => {
    document.documentElement.dataset.theme = localStorage.theme = {'light': 'dark', 'dark': 'light'}[document.documentElement.dataset.theme];
}

// Вспомогательные функции
const debounce = (func, delay) => {
    let timeout = null;
    return function(...args) {
        const later = () => {
            clearTimeout(timeout);
            func(...args);
        };
        clearTimeout(timeout);
        timeout = setTimeout(later, delay);
    };
}

// Функция выбора аватара
const avatar = ({ id }) => {
    [...document.querySelectorAll('.avatars button')].map(avatar => avatar.classList.toggle('opacity-50', avatar.dataset.avatarId !== id))
    document.querySelector('[name=avatar_id]').value = id;
}

// Функция поиска эпизодов
function searchEpisode(query) {
    if (query.length < 1) {
        document.getElementById('searchResults').style.display = 'none';
        document.getElementById('searchResults').innerHTML = '';
        return;
    }

    fetch('/search_episodes.php?term=' + encodeURIComponent(query))
        .then(response => response.json())
        .then(data => {
            let resultsContainer = document.getElementById('searchResults');
            resultsContainer.innerHTML = '';

            if (!data || !data.length) {
                resultsContainer.style.display = 'none';
                return;
            }

            const html = data.map(item => {
                let url = `/seasons-${item.season}-episode-${item.episode}`;

                return `
                    <li style="padding: 8px;">
                        <a href="${url}" class="hover:underline block">
                            Сезон ${item.season}, Серия ${item.episode}${
                                item.episode_title
                                    ? ': ' + item.episode_title
                                    : ''
                            }
                        </a>
                    </li>
                `;
            }).join('');

            resultsContainer.innerHTML = html;
            resultsContainer.style.display = 'block';
        })
        .catch(err => {
            console.error('Ошибка при поиске:', err);
        });
}

// Функции проверки и загрузки изображений
async function checkImageExists(url) {
    try {
        const response = await fetch(url, { method: 'HEAD' });
        return response.ok;
    } catch {
        return false;
    }
}

async function findPosterImage(domain) {
    const formats = ['jpeg', 'jpg', 'png', 'webp'];
    const baseUrl = `https://${domain}/img/poster-player.`;
    
    // Предварительная загрузка изображения
    const preloadImage = (url) => {
        return new Promise((resolve) => {
            const img = new Image();
            img.onload = () => resolve(true);
            img.onerror = () => resolve(false);
            img.src = url;
        });
    };
    
    for (const format of formats) {
        const imageUrl = baseUrl + format;
        const exists = await checkImageExists(imageUrl);
        
        if (exists) {
            // Начинаем предварительную загрузку изображения
            const preloaded = await preloadImage(imageUrl);
            if (preloaded) {
                // Если изображение успешно предзагружено, обновляем URL в плеере
                const iframe = document.querySelector('iframe');
                if (iframe) {
                    const currentSrc = iframe.src;
                    const encodedPosterUrl = encodeURIComponent(imageUrl);
                    const newSrc = currentSrc.replace(
                        /poster=[^&]*/,
                        `poster=${encodedPosterUrl}`
                    );
                    iframe.src = newSrc;
                }
                return imageUrl;
            }
        }
    }
    return null;
}

// Обработчик загрузки страницы - основной
document.addEventListener("DOMContentLoaded", () => {
    // Выбор первого аватара
    document.querySelector('.avatars button')?.click();
    
    // Инициализация плееров с отложенной загрузкой
    initializePlayers();
    
    // Настройка поиска
    setupSearchFunctionality();
    
    // Добавление стилей анимации если их нет
    if (!document.querySelector('style[data-animation="spin"]')) {
        const style = document.createElement('style');
        style.setAttribute('data-animation', 'spin');
        style.textContent = `
            @keyframes spin {
                from { transform: rotate(0deg); }
                to { transform: rotate(360deg); }
            }
        `;
        document.head.appendChild(style);
    }
});

/**
 * Инициализация плееров с отложенной загрузкой
 */
function initializePlayers() {
    // Флаги для отслеживания загруженных плееров
    const playerLoaded = {};
    // Хранение оригинальных URL для каждого плеера
    const playerSources = {};
    
    // Сохраняем исходные URL из iframe и очищаем контейнеры
    document.querySelectorAll('.player-container').forEach(container => {
        const playerId = container.id;
        playerLoaded[playerId] = false;
        
        const iframe = container.querySelector('iframe');
        if (iframe) {
            // Сохраняем URL источника
            playerSources[playerId] = iframe.src;
            
            // Получаем родительский div с классом 'relative'
            const playerDiv = iframe.parentElement;
            if (playerDiv) {
                // Удаляем iframe
                iframe.remove();
                // Добавляем ID для удобства обращения
                playerDiv.id = playerId + '-container';
            }
        }
    });
    
    // Функция для загрузки плеера
    function loadPlayer(playerId) {
        if (!playerLoaded[playerId] && playerSources[playerId]) {
            const containerElement = document.getElementById(playerId + '-container');
            if (!containerElement) return;
            
            // Создаем и добавляем локальный индикатор загрузки внутри контейнера плеера
            const spinner = document.createElement('div');
            spinner.className = 'player-spinner';
            spinner.innerHTML = `
                <div class="spinner-container">
                    <svg width="50" height="50" viewBox="0 0 50 50" style="animation: spin 1s linear infinite;">
                        <circle cx="25" cy="25" r="20" fill="none" stroke="#007bff" stroke-width="5" stroke-dasharray="60 30"></circle>
                    </svg>
                    <p style="margin-top: 10px; text-align: center;">Загрузка плеера...</p>
                </div>
            `;
            
            // Корректируем стили для идеального центрирования
            spinner.style.cssText = `
                position: absolute;
                top: 0;
                left: 0;
                right: 0;
                bottom: 0;
                width: 100%;
                height: 100%;
                display: flex;
                align-items: center;
                justify-content: center;
                background-color: rgba(0, 0, 0, 0.7);
                z-index: 10;
                margin: 0;
                padding: 0;
            `;
            
            spinner.querySelector('.spinner-container').style.cssText = `
                display: flex;
                flex-direction: column;
                align-items: center;
                justify-content: center;
                color: white;
                text-align: center;
                width: auto;
                margin: 0 auto;
            `;
            
            containerElement.appendChild(spinner);
            
            const iframe = document.createElement('iframe');
            iframe.src = playerSources[playerId];
            iframe.className = 'absolute inset-0 w-full h-full';
            iframe.frameBorder = '0';
            iframe.allowFullscreen = true;
            iframe.title = 'Видеоплеер ' + playerId.replace('player', '');
            
            // Скрываем индикатор загрузки после загрузки iframe
            iframe.onload = function() {
                // Удаляем локальный спиннер
                if (spinner && spinner.parentNode) {
                    spinner.parentNode.removeChild(spinner);
                }
            };
            
            containerElement.appendChild(iframe);
            playerLoaded[playerId] = true;
        }
    }
    
    // Загружаем сохраненный или активный плеер при загрузке страницы
    let activePlayerId;
    
    // Пытаемся получить сохраненный плеер из localStorage
    try {
        const savedPlayerId = localStorage.getItem('activePlayer');
        const episodeKey = document.getElementById('episode-title')?.getAttribute('data-episode-key');
        
        // Используем сохраненный плеер только если он есть и соответствует текущему эпизоду
        if (savedPlayerId && document.getElementById(savedPlayerId)) {
            activePlayerId = savedPlayerId;
            
            // Устанавливаем класс активности на кнопку плеера
            document.querySelectorAll('.tab-btn').forEach(btn => {
                btn.classList.remove('active');
                if (btn.getAttribute('data-player') === activePlayerId) {
                    btn.classList.add('active');
                }
            });
            
            // Показываем выбранный плеер, скрываем остальные
            document.querySelectorAll('.player-container').forEach(container => {
                if (container.id === activePlayerId) {
                    container.classList.remove('hidden');
                } else {
                    container.classList.add('hidden');
                }
            });
        } else {
            // Если сохраненного плеера нет или он не валиден, используем активный по умолчанию
            const activeTabButton = document.querySelector('.tab-btn.active');
            if (activeTabButton) {
                activePlayerId = activeTabButton.getAttribute('data-player');
            }
        }
    } catch (e) {
        // В случае ошибки используем активный плеер по умолчанию
        const activeTabButton = document.querySelector('.tab-btn.active');
        if (activeTabButton) {
            activePlayerId = activeTabButton.getAttribute('data-player');
        }
    }
    
    // Загружаем плеер
    if (activePlayerId) {
        loadPlayer(activePlayerId);
    }
    
    // Обработчики кликов на вкладки
    const tabButtons = document.querySelectorAll('.tab-btn');
    tabButtons.forEach(button => {
        button.addEventListener('click', function() {
            // Убираем класс active со всех кнопок
            tabButtons.forEach(btn => {
                btn.classList.remove('active');
            });
            
            // Скрываем все плееры
            const playerContainers = document.querySelectorAll('.player-container');
            playerContainers.forEach(container => {
                container.classList.add('hidden');
            });
            
            // Активируем выбранную вкладку
            this.classList.add('active');
            const playerId = this.getAttribute('data-player');
            const playerContainer = document.getElementById(playerId);
            if (playerContainer) {
                playerContainer.classList.remove('hidden');
                // Загружаем плеер по требованию
                loadPlayer(playerId);
                
                // Сохраняем выбор пользователя в localStorage
                try {
                    localStorage.setItem('activePlayer', playerId);
                } catch (e) {
                    // Тихая обработка ошибок
                }
            }
        });
    });
}

/**
 * Настройка функциональности поиска
 */
function setupSearchFunctionality() {
    const searchInput = document.getElementById('searchInput');
    const searchResults = document.getElementById('searchResults');
    
    if (searchInput && searchResults) {
        searchInput.addEventListener('input', function() {
            const query = this.value.trim();
            if (query.length > 0) {
                searchEpisode(query);
            } else {
                searchResults.style.display = 'none';
            }
        });
        
        // Закрываем результаты поиска при клике вне поля
        document.addEventListener('click', function(e) {
            if (!searchInput.contains(e.target) && !searchResults.contains(e.target)) {
                searchResults.style.display = 'none';
            }
        });
    }
}

// Скрипт для выбора аватаров (альтернативная версия)
const avatarButtons = document.querySelectorAll('.avatar-btn');
const avatarIdInput = document.getElementById('avatar_id');

avatarButtons.forEach(button => {
    button.addEventListener('click', function() {
        const avatarId = this.getAttribute('data-avatar-id');
        
        // Удаляем selected со всех кнопок
        avatarButtons.forEach(btn => btn.classList.remove('selected'));
        
        // Помечаем выбранную кнопку и обновляем скрытое поле
        this.classList.add('selected');
        if (avatarIdInput) {
            avatarIdInput.value = avatarId;
        }
    });
});

function kodik_translates_alt() {
    $('#translators-list').on('click','.b-translator__item',function() {
        var _self = $(this);
        if(!_self.hasClass('active')) {

            $('.b-translator__item').removeClass('active');
            _self.addClass('active');

            var this_link = _self.attr("data-this_link");
            $('#player_kodik').html('<iframe src="'+this_link+'" width="724" height="460" frameborder="0" allowfullscreen=""></iframe>');

        }
    });
}