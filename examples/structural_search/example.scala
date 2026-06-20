// Scala example for structural search testing.

/**
 * Config holds application configuration.
 */
case class Config(
  host: String = "localhost",
  port: Int = 8080,
  debug: Boolean = false
) {
  def url: String = s"http://$host:$port"
}

/**
 * Person represents a person.
 */
case class Person(name: String, age: Int) {
  def greet: String = s"Hello, I'm $name and I'm $age years old."
}

/**
 * Logger provides logging functionality.
 */
object Logger {
  def info(message: String): Unit = {
    println(s"[INFO] $message")
  }

  def error(message: String): Unit = {
    println(s"[ERROR] $message")
  }
}

/**
 * PersonService manages persons.
 */
class PersonService {
  private var persons: List[Person] = List.empty

  def addPerson(name: String, age: Int): Person = {
    val person = Person(name, age)
    persons = person :: persons
    Logger.info(s"Added person: $name")
    person
  }

  def getAllPersons: List[Person] = persons

  def getPersonCount: Int = persons.size

  def getPersonsByAge(minAge: Int, maxAge: Int): List[Person] = {
    persons.filter(p => p.age >= minAge && p.age <= maxAge)
  }

  def sortByAge: PersonService = {
    persons = persons.sortBy(_.age)
    this
  }
}

/**
 * Main application entry point.
 */
object Main extends App {
  val config = Config()
  Logger.info(s"Starting application at ${config.url}")

  val service = new PersonService()
  service.addPerson("Alice", 30)
  service.addPerson("Bob", 25)
  service.addPerson("Charlie", 35)

  service.sortByAge()

  for (person <- service.getAllPersons) {
    println(person.greet)
  }

  Logger.info(s"Total persons: ${service.getPersonCount}")
}
