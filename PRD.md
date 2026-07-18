# mc-server-cli

## Propósito del producto
Una TUI hecha en GO para gestionar localmente servidores de Minecraft
## Qué resuelve
Las dificultades que tienen los usuarios generalmente para instalar servidores y configurarlos manualmente en su propia computadora
## Público objetivo
Personas que tengan PC y conozcan un poco cómo funcionan las interfaces de comandos
## Funcionalidades
A continuación se detallan las funcionalidades del producto a realizar:
* R1: Gestión de ciclos: Botones rápidos para iniciar, detener y reiniciar instancias del servidor.
* R2: Consola interactiva: Vista en tiempo real del log con una barra inferior para enviar comandos directos al servidor.
* R3: Administrador de archivos: Panel para editar el archivo server.properties y gestionar carpetas de mundos o plugins.
* R4: Selector de versiones: Descarga automática de archivos .jar (Vanilla, Paper, Purpur, Fabric) desde sus respectivas API oficiales
* R5: Panel principal con métricas de uso de cpu, ram y memoria de cada instancia que esté encendida.
* R6: Búsqueda e instalación mediante la TUI de plugins, mods, datapacks, etc, desde las respectivas API oficiales de Modrinth u otros gestores, para cada servidor independiente dependiendo de los archivos (si son Java, Paper, Purpur, Fabric) y sus versiones respectivas para seleccionar.
* R7: Compilación para instalación mediante CLI para Windows y Linux.
## Alcance y límites
* Dentro del alcance: Control de servidores locales en la misma máquina, lectura de logs por buffer y soporte para múltiples instancias guardadas en rutas locales.
* Fuera del alcance: Conexiones SSH/SFTP remotas, instalación automática de dependencias del sistema (como Java) y despliegue en entornos Docker (para mantener la TUI ligera y nativa).
## Métricas de éxito
* Test con cobertura 100% del código
* Auditoría de usabilidad por el propio agente
* TDD: Test, Code, Refactor, calidad de código se valora
## Diseño y requerimientos técnicos
* Librerías TUI: Uso de Bubble Tea (arquitectura Elm para el estado), Bubbles (componentes de texto/listas) y Lip Gloss (estilos y colores ANSI).
* Concurrencia: Uso de Goroutines y Channels de Go para leer el stdout del proceso de Minecraft sin congelar la interfaz gráfica.
* Persistencia: Un archivo de configuración simple en formato JSON o TOML para almacenar las rutas y configuraciones de cada servidor.
* Para la documentación: https://context7.com/grindlemire/go-tui