// Navegación por hash (#/…): no requiere servidor de rutas ni SvelteKit.
//   #/            resumen y tabla
//   #/c/<id>      detalle de un contenedor (ID completo)
//   #/incidents   historial de incidentes y pendientes
//   #/rules       reglas de alerta
//   #/maintenance modo mantenimiento

export type Route =
  | { name: 'overview' }
  | { name: 'container'; id: string }
  | { name: 'incidents' }
  | { name: 'rules' }
  | { name: 'maintenance' }
  | { name: 'not_found'; path: string };

function parse(hash: string): Route {
  const path = hash.replace(/^#/, '') || '/';
  if (path === '/') return { name: 'overview' };
  if (path === '/incidents') return { name: 'incidents' };
  if (path === '/rules') return { name: 'rules' };
  if (path === '/maintenance') return { name: 'maintenance' };
  const m = /^\/c\/([A-Za-z0-9][A-Za-z0-9_.-]{0,127})$/.exec(path);
  if (m?.[1]) return { name: 'container', id: m[1] };
  return { name: 'not_found', path };
}

class Router {
  route = $state<Route>(parse(location.hash));

  constructor() {
    addEventListener('hashchange', () => {
      this.route = parse(location.hash);
      scrollTo(0, 0);
    });
  }
}

export const router = new Router();

export const containerHref = (id: string) => `#/c/${encodeURIComponent(id)}`;
