// TypeScript example for structural search testing.

/**
 * Config holds application configuration.
 */
interface Config {
  host: string;
  port: number;
  debug: boolean;
}

/**
 * Person represents a person.
 */
interface Person {
  name: string;
  age: number;
}

/**
 * Logger provides logging functionality.
 */
class Logger {
  static info(message: string): void {
    console.log(`[INFO] ${message}`);
  }

  static error(message: string): void {
    console.error(`[ERROR] ${message}`);
  }
}

/**
 * PersonService manages persons.
 */
class PersonService {
  private persons: Person[] = [];

  addPerson(name: string, age: number): Person {
    const person: Person = { name, age };
    this.persons.push(person);
    Logger.info(`Added person: ${name}`);
    return person;
  }

  getAllPersons(): Person[] {
    return [...this.persons];
  }

  getPersonCount(): number {
    return this.persons.length;
  }

  getPersonsByAge(minAge: number, maxAge: number): Person[] {
    return this.persons.filter(
      (p) => p.age >= minAge && p.age <= maxAge
    );
  }

  sortByName(): PersonService {
    this.persons.sort((a, b) => a.name.localeCompare(b.name));
    return this;
  }
}

/**
 * createConfig creates a Config with defaults.
 */
function createConfig(): Config {
  return {
    host: "localhost",
    port: 3000,
    debug: false,
  };
}

/**
 * greet returns a greeting string.
 */
function greet(person: Person): string {
  return `Hello, ${person.name}! You are ${person.age} years old.`;
}

// Main execution.
const config: Config = createConfig();
Logger.info(`Starting application at ${config.host}:${config.port}`);

const service = new PersonService();
service.addPerson("Alice", 30);
service.addPerson("Bob", 25);
service.addPerson("Charlie", 35);

service.sortByName();

for (const person of service.getAllPersons()) {
  console.log(greet(person));
}

Logger.info(`Total persons: ${service.getPersonCount()}`);
