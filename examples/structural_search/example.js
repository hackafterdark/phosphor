// JavaScript example for structural search testing.

/**
 * Config holds application configuration.
 * @typedef {Object} Config
 * @property {string} host
 * @property {number} port
 * @property {boolean} debug
 */

/**
 * Person represents a person.
 * @typedef {Object} Person
 * @property {string} name
 * @property {number} age
 */

/**
 * Logger provides logging functionality.
 */
const Logger = {
  info(message) {
    console.log(`[INFO] ${message}`);
  },

  error(message) {
    console.error(`[ERROR] ${message}`);
  },
};

/**
 * PersonService manages persons.
 */
class PersonService {
  constructor() {
    this.persons = [];
  }

  /**
   * Add a person.
   * @param {string} name
   * @param {number} age
   * @returns {Person}
   */
  addPerson(name, age) {
    const person = { name, age };
    this.persons.push(person);
    Logger.info(`Added person: ${name}`);
    return person;
  }

  /**
   * Get all persons.
   * @returns {Person[]}
   */
  getAllPersons() {
    return [...this.persons];
  }

  /**
   * Get person count.
   * @returns {number}
   */
  getPersonCount() {
    return this.persons.length;
  }

  /**
   * Get persons by age range.
   * @param {number} minAge
   * @param {number} maxAge
   * @returns {Person[]}
   */
  getPersonsByAge(minAge, maxAge) {
    return this.persons.filter(
      (p) => p.age >= minAge && p.age <= maxAge
    );
  }

  /**
   * Sort persons by name.
   * @returns {PersonService}
   */
  sortByName() {
    this.persons.sort((a, b) => a.name.localeCompare(b.name));
    return this;
  }
}

/**
 * Create a config with defaults.
 * @returns {Config}
 */
function createConfig() {
  return {
    host: "localhost",
    port: 3000,
    debug: false,
  };
}

/**
 * Greet a person.
 * @param {Person} person
 * @returns {string}
 */
function greet(person) {
  return `Hello, ${person.name}! You are ${person.age} years old.`;
}

/**
 * Fetch persons from a remote source.
 * @returns {Promise<Person[]>}
 */
async function fetchPersons() {
  try {
    const response = await fetch("/api/persons");
    const data = await response.json();
    return data;
  } catch (error) {
    Logger.error(`Failed to fetch persons: ${error.message}`);
    return [];
  }
}

/**
 * Load persons with error handling.
 * @returns {Promise<void>}
 */
async function loadPersons() {
  try {
    const persons = await fetchPersons();
    for (const p of persons) {
      service.addPerson(p.name, p.age);
    }
  } catch (err) {
    Logger.error(`Load failed: ${err.message}`);
  }
}

// Main execution.
const config = createConfig();
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
