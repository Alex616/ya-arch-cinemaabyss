## Изучите [README.md](README.md) файл и структуру проекта.

## Задание 1

1. Была спроектирована архитектура системы КиноБездны с применением микросервисов и событийной шины.

Основные домены:

- Пользователи (User)
- Биллинг (Payment)
- Каталог фильмов (Catalog)
- Рекомендации (Recommendations)
- Система хранения файлов (File Storage)

[Диаграмма контейнеров](diagrams/containers/container_Cinemaabyss.png)

## Задание 2

[результаты тестов](images/local-test-result.png)

[состояние топиков](images/topics-info.png)

## Задание 3

[скриншот обработки событий](images/events-service-logs.png)

[скриншот вывода при вызове https://cinemaabyss.example.com/api/movies](images/movies-api-result.png)

[скриншот тестов ](images/test-results.png)

## Задание 4

[скриншот апи movies](images/helm-movies-api.png)

[скриншот разворачивания helm](images/helm-install.png)

## Задание 5

[скриншот работы circuit breaker'а](images/circuit-breaker-work.png)

[скриншот работы fortio](images/fortio-test.png)
