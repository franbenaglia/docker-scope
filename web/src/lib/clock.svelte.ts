// Reloj compartido (1 s) para tiempos relativos que deben avanzar entre eventos.
class Clock {
  now = $state(Date.now());
  #timer: ReturnType<typeof setInterval> | null = null;
  #users = 0;

  /** Activa el reloj mientras haya al menos un componente que lo use. */
  use(): () => void {
    if (this.#users++ === 0) this.#timer = setInterval(() => (this.now = Date.now()), 1000);
    return () => {
      if (--this.#users === 0 && this.#timer) clearInterval(this.#timer);
    };
  }
}

export const clock = new Clock();

/** Segundos transcurridos desde iso según el reloj compartido. */
export const since = (iso: string) => (clock.now - Date.parse(iso)) / 1000;
